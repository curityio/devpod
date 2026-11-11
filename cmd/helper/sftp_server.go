package helper

import (
	"errors"
	"io"
	"os"

	"github.com/curityio/devpod/cmd/flags"
	"github.com/curityio/devpod/pkg/stdio"
	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
)

// NewSFTPServerCmd creates a command that serves SFTP over stdin and stdout, like OpenSSH's sftp-server. The SSH
// server runs it for SFTP sessions in the user's home directory, which relative paths are relative to.
func NewSFTPServerCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "sftp-server",
		Short: "Serves SFTP over stdin and stdout",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			// stdout is the SFTP stream, so nothing else may be written to it
			var options []sftp.ServerOption
			if globalFlags.Debug {
				options = append(options, sftp.WithDebug(os.Stderr))
			}
			server, err := sftp.NewServer(stdio.NewStdioStream(os.Stdin, os.Stdout, false, 0), options...)
			if err != nil {
				return err
			}
			defer server.Close()

			err = server.Serve()
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
}
