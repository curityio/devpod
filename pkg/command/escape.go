package command

import (
	"github.com/alessio/shellescape"
)

func Quote(args []string) string {
	if len(args) == 0 {
		return ""
	} else if len(args) == 1 {
		return args[0]
	}

	return shellescape.QuoteCommand(args)
}

// AsUser returns a shell command that runs the shell command command as user with su, or command itself when user
// is empty or root. Both command and user are quoted, so they may contain any character.
func AsUser(user, command string) string {
	if user == "" || user == "root" {
		return command
	}

	return "su -c " + shellescape.Quote(command) + " " + shellescape.Quote(user)
}
