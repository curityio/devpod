package helper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/curityio/devpod/cmd/flags"
	"github.com/curityio/devpod/pkg/stdio"
	"github.com/pkg/sftp"
	"github.com/spf13/cobra"
)

// NewSFTPServerCmd creates a command that serves SFTP over stdin and stdout, like OpenSSH's sftp-server. The SSH
// server runs it for SFTP sessions in the user's home directory, which relative paths are relative to.
func NewSFTPServerCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	var umask string
	cmd := &cobra.Command{
		Use:   "sftp-server",
		Short: "Serves SFTP over stdin and stdout",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if umask != "" {
				mask, err := strconv.ParseUint(umask, 8, 32)
				if err != nil {
					return fmt.Errorf("parse umask %q: %w", umask, err)
				}
				// the server ignores the mode clients ask for, so this is the only way to create private files
				setUmask(int(mask))
			}

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
	cmd.Flags().StringVar(&umask, "umask", "", "The umask, in octal, to create files and directories with")
	return cmd
}
