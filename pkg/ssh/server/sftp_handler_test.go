package server

import "testing"

func TestSFTPUmask(t *testing.T) {
	tests := map[string][]string{
		"":    {"HOME=/home/dev"},
		"077": {"HOME=/home/dev", "DEVPOD_SFTP_UMASK=077"},
		"0":   {"DEVPOD_SFTP_UMASK=0"},
	}
	for expected, environ := range tests {
		if got := sftpUmask(environ); got != expected {
			t.Errorf("expected %q from %v, got %q", expected, environ, got)
		}
	}
	for _, invalid := range []string{"", "8", "abc", "1777", "077 --debug"} {
		if got := sftpUmask([]string{"DEVPOD_SFTP_UMASK=" + invalid}); got != "" {
			t.Errorf("expected invalid umask %q to be ignored, got %q", invalid, got)
		}
	}
}
