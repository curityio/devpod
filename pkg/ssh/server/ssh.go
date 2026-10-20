package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"

	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/shell"
	"github.com/loft-sh/devpod/pkg/ssh/sshd"
)

const DefaultPort int = 8022

type Server interface {
	Serve(listener net.Listener) error
	ListenAndServe() error
}

type server struct {
	currentUser string
	shell       []string
	workdir     string
	reuseSock   string
	sshServer   sshd.Server
	log         log.Logger
}

func NewServer(addr string, hostKey []byte, keys []sshd.PublicKey, workdir string, reuseSock string, log log.Logger) (Server, error) {
	sh, err := shell.GetShell("")
	if err != nil {
		return nil, err
	}

	currentUser, err := user.Current()
	if err != nil {
		return nil, err
	}

	forwardHandler := &sshd.ForwardedTCPHandler{}
	forwardedUnixHandler := &sshd.ForwardedUnixHandler{}
	server := &server{
		shell:       sh,
		workdir:     workdir,
		reuseSock:   reuseSock,
		log:         log,
		currentUser: currentUser.Username,
		sshServer: sshd.Server{
			Addr: addr,
			LocalPortForwardingCallback: func(ctx sshd.Context, dhost string, dport uint32) bool {
				log.Debugf("Accepted forward: %s:%d", dhost, dport)
				return true
			},
			ReversePortForwardingCallback: func(ctx sshd.Context, host string, port uint32) bool {
				log.Debugf("attempt to bind %s:%d - %s", host, port, "granted")
				return true
			},
			ReverseUnixForwardingCallback: func(ctx sshd.Context, socketPath string) bool {
				log.Debugf("attempt to bind socket %s", socketPath)

				err := removeStaleSocket(socketPath)
				if err != nil {
					log.Debugf("refuse to bind socket %s: %v", socketPath, err)
					return false
				}

				return true
			},
			ChannelHandlers: map[string]sshd.ChannelHandler{
				"direct-tcpip":                   sshd.DirectTCPIPHandler,
				"direct-streamlocal@openssh.com": sshd.DirectStreamLocalHandler,
				"session":                        sshd.DefaultSessionHandler,
			},
			RequestHandlers: map[string]sshd.RequestHandler{
				"tcpip-forward":                          forwardHandler.HandleSSHRequest,
				"streamlocal-forward@openssh.com":        forwardedUnixHandler.HandleSSHRequest,
				"cancel-streamlocal-forward@openssh.com": forwardedUnixHandler.HandleSSHRequest,
				"cancel-tcpip-forward":                   forwardHandler.HandleSSHRequest,
			},
			SubsystemHandlers: map[string]sshd.SubsystemHandler{
				"sftp": func(s sshd.Session) {
					sftpHandler(s, currentUser.Username, log)
				},
			},
		},
	}

	if len(keys) > 0 {
		server.sshServer.PublicKeyHandler = func(ctx sshd.Context, key sshd.PublicKey) bool {
			for _, k := range keys {
				if sshd.KeysEqual(k, key) {
					return true
				}
			}

			log.Debugf("Declined public key")
			return false
		}
	}

	if len(hostKey) > 0 {
		err = server.sshServer.SetOption(sshd.HostKeyPEM(hostKey))
		if err != nil {
			return nil, err
		}
	}

	server.sshServer.Handler = server.handler
	return server, nil
}

func (s *server) handler(sess sshd.Session) {
	var err error
	ptyReq, winCh, isPty := sess.Pty()
	cmd := s.getCommand(sess, isPty)

	if sshd.AgentRequested(sess) {
		l, cleanup, err := setupAgentListener(s.reuseSock)
		if err != nil {
			exitWithError(sess, err, s.log)
			return
		}
		defer cleanup()

		go sshd.ForwardAgentConnections(l, sess)

		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "SSH_AUTH_SOCK", l.Addr().String()))
	}

	// start shell session
	if isPty {
		err = execPTY(sess, ptyReq, winCh, cmd, s.log)
	} else {
		err = execNonPTY(sess, cmd, s.log)
	}

	// exit session
	exitWithError(sess, err, s.log)
}

func (s *server) getCommand(sess sshd.Session, isPty bool) *exec.Cmd {
	var cmd *exec.Cmd
	user := sess.User()
	if user == s.currentUser {
		user = ""
	}

	// has user set?
	if user != "" {
		args := []string{}

		// is pty?
		if isPty {
			args = append(args, "-")
		}

		// add user
		args = append(args, sess.User())

		// is there a command?
		if len(sess.RawCommand()) > 0 {
			args = append(args, "-c", sess.RawCommand())
		}

		cmd = exec.Command("su", args...)
	} else {
		args := []string{}
		args = append(args, s.shell[1:]...)
		if isPty {
			args = append(args, "-l")
		}

		if len(sess.RawCommand()) == 0 {
			cmd = exec.Command(s.shell[0], args...)
		} else {
			args = append(args, "-c", sess.RawCommand())
			cmd = exec.Command(s.shell[0], args...)
		}
	}

	cmd.Dir = findWorkdir(s.workdir, user)
	cmd.Env = append(cmd.Env, os.Environ()...)
	cmd.Env = append(cmd.Env, sess.Environ()...)
	return cmd
}

func (s *server) Serve(listener net.Listener) error {
	return s.sshServer.Serve(listener)
}

func (s *server) ListenAndServe() error {
	s.log.Debugf("Start ssh server on %s", s.sshServer.Addr)
	return s.sshServer.ListenAndServe()
}
