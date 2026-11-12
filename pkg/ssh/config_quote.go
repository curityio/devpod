package ssh

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// safeProxyCommandArg matches the arguments a ProxyCommand can hold without quoting on every platform.
var safeProxyCommandArg = regexp.MustCompile(`^[A-Za-z0-9@+=:,./_-]+$`)

// safeWindowsProxyCommandArg also allows backslashes, which Windows only treats specially before a quote.
var safeWindowsProxyCommandArg = regexp.MustCompile(`^[A-Za-z0-9@+=:,./_\\-]+$`)

// proxyCommandLine joins args into a ProxyCommand value that OpenSSH on goos runs as the program args[0] with
// the arguments args[1:].
//
// The value goes through three parsers, and the quoting must survive all of them:
//   - OpenSSH splits every config line into words (honoring '...', "..." and the escapes \\, \', \" and \ ),
//     and rejects the whole config file if a quote isn't closed. It then uses the rest of the ProxyCommand line
//     verbatim.
//   - It expands the % tokens (%h, %p, ...) in it, so a literal % must be written as %%.
//   - On Unix it runs `$SHELL -c "exec VALUE"`, so VALUE must be shell-quoted. Win32-OpenSSH has no shell: it
//     passes VALUE to CreateProcess as the command line (it adds quotes around the program unless VALUE starts
//     with one), and the program (devpod.exe is a Go program) splits it with the Windows C runtime's rules.
func proxyCommandLine(goos string, args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if goos == "windows" {
			// Win32-OpenSSH only passes the command line on unchanged if it starts with a quote.
			quoted[i] = quoteWindowsArg(arg, i == 0)
		} else {
			quoted[i] = quoteUnixArg(arg)
		}
	}
	return strings.ReplaceAll(strings.Join(quoted, " "), "%", "%%")
}

// quoteUnixArg quotes s as one word for a POSIX shell (and fish). Single quotes alone, as shellescape.Quote
// writes them, aren't enough: OpenSSH's config parser treats \' inside '...' as an escaped quote, so a value
// like `a\` (quoted as 'a\') leaves the quote unclosed and breaks the whole config file. Backslashes and single
// quotes are therefore escaped outside the single quotes, where the shell and OpenSSH agree on \\ and \'.
func quoteUnixArg(s string) string {
	if safeProxyCommandArg.MatchString(s) {
		return s
	}
	if s == "" {
		return "''"
	}
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		if r == '\'' || r == '\\' {
			if inQuote {
				b.WriteByte('\'')
				inQuote = false
			}
			b.WriteByte('\\')
		} else if !inQuote {
			b.WriteByte('\'')
			inQuote = true
		}
		b.WriteRune(r)
	}
	if inQuote {
		b.WriteByte('\'')
	}
	return b.String()
}

// quoteWindowsArg quotes s as one argument of a Windows command line, like syscall.EscapeArg (which only exists
// on Windows), but also quotes arguments that contain a single quote, which OpenSSH's config parser would read
// as the start of a quoted string, and quotes s if always is set.
func quoteWindowsArg(s string, always bool) string {
	if !always && safeWindowsProxyCommandArg.MatchString(s) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			// Backslashes before a quote are escapes, so double them, and escape the quote.
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	// The closing quote must not be escaped by trailing backslashes.
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// splitProxyCommandLine splits a ProxyCommand value written by proxyCommandLine for goos back into its arguments.
// It also reads the values older versions of DevPod wrote in double quotes.
func splitProxyCommandLine(goos, value string) []string {
	value = strings.ReplaceAll(value, "%%", "%")
	if goos == "windows" {
		return windowsCommandLineToArgv(value)
	}
	return splitUnixWords(value)
}

// splitUnixWords splits s into words like a POSIX shell, without expanding anything.
func splitUnixWords(s string) []string {
	var words []string
	var b strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, b.String())
				b.Reset()
				inWord = false
			}
			continue
		case c == '\'':
			// everything up to the next single quote is literal
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				end = len(s) - i - 1
			}
			b.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case c == '"':
			// in double quotes, a backslash only escapes ", \, $ and `
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
			}
		case c == '\\' && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(c)
		}
		inWord = true
	}
	if inWord {
		words = append(words, b.String())
	}
	return words
}

// windowsCommandLineToArgv splits a command line like a Go program on Windows does (os.commandLineToArgv).
func windowsCommandLineToArgv(cmd string) []string {
	var args []string
	for len(cmd) > 0 {
		if cmd[0] == ' ' || cmd[0] == '\t' {
			cmd = cmd[1:]
			continue
		}
		var b []byte
		inquote := false
		nslash := 0
	arg:
		for ; len(cmd) > 0; cmd = cmd[1:] {
			c := cmd[0]
			switch c {
			case ' ', '\t':
				if !inquote {
					break arg
				}
			case '"':
				b = append(b, bytes.Repeat([]byte{'\\'}, nslash/2)...)
				if nslash%2 == 0 {
					// a doubled quote in quotes is a literal quote
					if inquote && len(cmd) > 1 && cmd[1] == '"' {
						b = append(b, c)
						cmd = cmd[1:]
					}
					inquote = !inquote
				} else {
					b = append(b, c)
				}
				nslash = 0
				continue
			case '\\':
				nslash++
				continue
			}
			b = append(b, bytes.Repeat([]byte{'\\'}, nslash)...)
			nslash = 0
			b = append(b, c)
		}
		args = append(args, string(append(b, bytes.Repeat([]byte{'\\'}, nslash)...)))
	}
	return args
}

// checkSSHConfigValue returns an error if value can't be written into an SSH config line: a line break would end
// the line and let the rest of value add its own settings.
func checkSSHConfigValue(name, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("can't write the %s %q into the SSH config: it contains a line break or NUL character", name, value)
	}
	return nil
}
