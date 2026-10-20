package server

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	copypkg "github.com/loft-sh/devpod/pkg/copy"
	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/devpod/pkg/log"
	shellpkg "github.com/loft-sh/devpod/pkg/shell"
	"github.com/loft-sh/devpod/pkg/ssh/sshd"
)

func NewContainerServer(addr string, workdir string, log log.Logger) (Server, error) {
	forwardHandler := &sshd.ForwardedTCPHandler{}
	forwardedUnixHandler := &sshd.ForwardedUnixHandler{}
	server := &containerServer{
		workdir: workdir,
		log:     log,
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

				_, err := os.Stat(socketPath)
				if err == nil {
					log.Debugf("%s already exists, removing", socketPath)

					_ = os.Remove(socketPath)
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
					sftpHandler(s, "", log)
				},
			},
		},
	}

	server.sshServer.Handler = server.handler
	return server, nil
}

type containerServer struct {
	sshServer sshd.Server
	log       log.Logger
	workdir   string
}

func (s *containerServer) Serve(listener net.Listener) error {
	return s.sshServer.Serve(listener)
}

func (s *containerServer) ListenAndServe() error {
	s.log.Debugf("Start ssh server on %s", s.sshServer.Addr)
	return s.sshServer.ListenAndServe()
}

func (s *containerServer) handler(sess sshd.Session) {
	var err error
	ptyReq, winCh, isPty := sess.Pty()
	cmd, err := s.getCommand(sess, isPty)
	if err != nil {
		exitWithError(sess, fmt.Errorf("get command: %w", err), s.log)
		return
	}

	if sshd.AgentRequested(sess) {
		l, tmpDir, err := setupAgentListener(sess, "")
		if err != nil {
			exitWithError(sess, err, s.log)
			return
		}
		defer l.Close()
		defer os.RemoveAll(tmpDir)

		err = chownListener(l.Addr().String(), sess.User())
		if err != nil {
			exitWithError(sess, fmt.Errorf("chown listener: %w", err), s.log)
			return
		}

		go sshd.ForwardAgentConnections(l, sess)

		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", "SSH_AUTH_SOCK", l.Addr().String()))
	}

	if isPty {
		err = execPTY(sess, ptyReq, winCh, cmd, s.log)
	} else {
		err = execNonPTY(sess, cmd, s.log)
	}

	exitWithError(sess, err, s.log)
}

func (s *containerServer) getCommand(sess sshd.Session, isPty bool) (*exec.Cmd, error) {
	var cmd *exec.Cmd
	user := sess.User()

	// get login shell for user
	shell, err := shellpkg.GetShell(user)
	if err != nil {
		return cmd, fmt.Errorf("get shell for user %s: %w", user, err)
	}

	args := []string{}
	args = append(args, shell[1:]...)
	if isPty {
		args = append(args, "-l")
	}

	if len(sess.RawCommand()) == 0 {
		cmd = exec.Command(shell[0], args...)
	} else {
		args = append(args, "-c", sess.RawCommand())
		cmd = exec.Command(shell[0], args...)
	}

	err = config.PrepareCmdUser(cmd, user)
	if err != nil {
		return cmd, fmt.Errorf("prepare cmd env: %w", err)
	}
	cmd.Dir = findWorkdir(s.workdir, user)
	cmd.Env = append(cmd.Env, sess.Environ()...)
	return cmd, nil
}

func chownListener(listenerPath string, user string) error {
	err := copypkg.Chown(filepath.Dir(listenerPath), user)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	err = copypkg.Chown(listenerPath, user)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
