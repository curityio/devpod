package ssh

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// awkwardProxyCommandValues are values that each of OpenSSH's config parser, its % token expansion, a shell and
// the Windows command line parser treat specially.
var awkwardProxyCommandValues = []string{
	"/work/o'brien $x proj",
	`/work/a\`,
	`a\'b`,
	`'`,
	`"`,
	`a\"b`,
	`trailing\\`,
	"$(touch pwned) `touch pwned` ${HOME}",
	"%h %p %% %",
	"#not a comment",
	"tab\tand  spaces",
	"* ? [a] ~ ! & | ; < > ( ) { }",
	`C:\Users\me\My Projects\x`,
	"ünïcödé",
	"-dash",
}

// proxyCommandOf returns the ProxyCommand value in config.
func proxyCommandOf(t *testing.T, config string) string {
	t.Helper()
	for _, line := range strings.Split(config, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "ProxyCommand "); ok {
			return strings.TrimSuffix(value, "\r")
		}
	}
	t.Fatalf("no ProxyCommand in %s", config)
	return ""
}

// proxyCommandTestUser is the user the tests use: the User line isn't quoted, and user names don't need it.
const proxyCommandTestUser = "vscode"

func expectedProxyCommandArgs(execPath, value string) []string {
	return []string{execPath, "ssh", "--stdio", "--context", value, "--user", proxyCommandTestUser, value, "--devpod-home", value, "--workdir", value}
}

// TestProxyCommandUnix has OpenSSH run the ProxyCommand with each shell installed, and checks the arguments the
// devpod executable gets.
func TestProxyCommandUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a Unix shell")
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		t.Skip("needs ssh")
	}

	// the devpod executable's path is awkward too
	binDir := filepath.Join(t.TempDir(), `bin o'brien $x \ " %h`)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(binDir, "devpod")
	script := "#!/bin/sh\nfor a in \"$0\" \"$@\"; do printf '%s\\0' \"$a\"; done > \"$DEVPOD_TEST_OUT\"\n"
	if err := os.WriteFile(execPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	shells := []string{}
	for _, shell := range []string{"sh", "bash", "zsh", "dash", "fish"} {
		if path, err := exec.LookPath(shell); err == nil {
			shells = append(shells, path)
		}
	}

	for _, value := range awkwardProxyCommandValues {
		config, err := addHostSection(runtime.GOOS, "", execPath, "testhost", proxyCommandTestUser, value, value, value, false, true, value)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config")
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}

		for _, shell := range shells {
			t.Run(filepath.Base(shell)+"/"+value, func(t *testing.T) {
				out := filepath.Join(dir, "args")
				_ = os.Remove(out)
				cmd := exec.Command(sshPath, "-F", configPath, "-o", "BatchMode=yes", "testhost", "true")
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "SHELL="+shell, "DEVPOD_TEST_OUT="+out)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				// ssh fails once the ProxyCommand exits without speaking SSH
				_ = cmd.Run()

				got, err := os.ReadFile(out)
				if err != nil {
					t.Fatalf("the ProxyCommand didn't run: %v\nssh: %s\nconfig:\n%s", err, stderr.String(), config)
				}
				args := strings.Split(strings.TrimSuffix(string(got), "\x00"), "\x00")
				if diff := cmp.Diff(expectedProxyCommandArgs(execPath, value), args); diff != "" {
					t.Errorf("ProxyCommand %s got wrong arguments (-want +got):\n%s", proxyCommandOf(t, config), diff)
				}
				if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
					t.Errorf("ProxyCommand %s ran a command substitution", proxyCommandOf(t, config))
				}
			})
		}
	}
}

// TestProxyCommandWindows checks that OpenSSH accepts the config written for Windows, and that the Windows
// command line Win32-OpenSSH starts devpod.exe with splits into the right arguments.
func TestProxyCommandWindows(t *testing.T) {
	sshPath, _ := exec.LookPath("ssh")
	execPath := `C:\Program Files\DevPod\devpod.exe`
	for _, value := range awkwardProxyCommandValues {
		t.Run(value, func(t *testing.T) {
			config, err := addHostSection("windows", "", execPath, "testhost", proxyCommandTestUser, value, value, value, false, true, value)
			if err != nil {
				t.Fatal(err)
			}
			proxyCommand := proxyCommandOf(t, config)

			if sshPath != "" {
				configPath := filepath.Join(t.TempDir(), "config")
				if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.Command(sshPath, "-F", configPath, "-G", "testhost").CombinedOutput()
				if err != nil {
					t.Fatalf("ssh rejects the config: %v: %s\n%s", err, out, config)
				}
				if !strings.Contains(string(out), "\nproxycommand "+proxyCommand+"\n") {
					t.Errorf("ssh read a different ProxyCommand than %s:\n%s", proxyCommand, out)
				}
			}

			// every % is doubled, and Win32-OpenSSH passes a command line that starts with a quote on unchanged
			commandLine := strings.ReplaceAll(proxyCommand, "%%", "%")
			if !strings.HasPrefix(commandLine, `"`) {
				t.Fatalf("ProxyCommand %s doesn't start with a quote", proxyCommand)
			}
			if diff := cmp.Diff(expectedProxyCommandArgs(execPath, value), windowsCommandLineToArgv(commandLine)); diff != "" {
				t.Errorf("ProxyCommand %s splits into wrong arguments (-want +got):\n%s", proxyCommand, diff)
			}
		})
	}
}

func TestAddHostSectionRejectsLineBreaks(t *testing.T) {
	for _, value := range []string{"a\nProxyCommand evil", "a\rb", "a\x00b"} {
		if _, err := addHostSection("linux", "", "/path/to/exec", "testhost", "user", "context", "workspace", value, false, true, ""); err == nil {
			t.Errorf("addHostSection accepted the workdir %q", value)
		}
	}
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
