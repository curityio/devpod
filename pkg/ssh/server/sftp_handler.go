package server

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/curityio/devpod/pkg/command"
	"github.com/curityio/devpod/pkg/log"
	"github.com/curityio/devpod/pkg/ssh/sshd"
	"github.com/sirupsen/logrus"
)

// SFTPUmaskEnv is an environment variable an SFTP client can set on its session, before requesting the sftp
// subsystem, to have files and directories created with that umask (in octal), such as 077 to create them private.
// It's not the default because the server ignores the permissions clients ask for when they create files.
const SFTPUmaskEnv = "DEVPOD_SFTP_UMASK"

// sftpUmask returns the valid SFTPUmaskEnv in environ, or "".
func sftpUmask(environ []string) string {
	for _, env := range environ {
		value, found := strings.CutPrefix(env, SFTPUmaskEnv+"=")
		if !found {
			continue
		}
		if mask, err := strconv.ParseUint(value, 8, 32); err != nil || mask > 0o777 {
			return ""
		}
		return value
	}
	return ""
}

// sftpHandler serves an SFTP session from a `helper sftp-server` process started in the user's home directory, the
// way OpenSSH runs sftp-server, so that relative paths are relative to the home directory. It doesn't serve SFTP
// in-process with sftp.WithServerWorkingDirectory, because that also makes relative symlink targets absolute.
func sftpHandler(sess sshd.Session, currentUser string, log log.Logger) {
	user := sess.User()
	if user == currentUser {
		user = ""
	}

	workingDir, err := command.GetHome(user)
	if err != nil {
		log.Debugf("sftp server: find home directory of %q: %v", user, err)
	}

	executable, err := os.Executable()
	if err != nil {
		log.Debugf("sftp server: find executable: %v", err)
		_ = sess.Exit(1)
		return
	}

	args := []string{"helper", "sftp-server"}
	if log.GetLevel() == logrus.DebugLevel {
		args = append(args, "--debug")
	}
	if umask := sftpUmask(sess.Environ()); umask != "" {
		args = append(args, "--umask", umask)
	}

	writer := log.Writer(logrus.DebugLevel, false)
	defer writer.Close()

	cmd := exec.Command(executable, args...)
	cmd.Dir = workingDir
	cmd.Stdin = sess
	cmd.Stdout = sess
	cmd.Stderr = writer
	err = cmd.Run()
	if err != nil {
		log.Debugf("sftp server completed with error: %v", err)
		_ = sess.Exit(1)
		return
	}
	_ = sess.Exit(0)
}
