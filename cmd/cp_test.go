package cmd

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseCpLocation(t *testing.T) {
	tests := []struct {
		arg       string
		goos      string
		workspace string
		path      string
		err       bool
	}{
		{arg: "my-file.txt", path: "my-file.txt"},
		{arg: "./here/file-here.txt", path: "./here/file-here.txt"},
		{arg: "foo:", workspace: "foo", path: ""},
		{arg: "foo:file-in-container.txt", workspace: "foo", path: "file-in-container.txt"},
		{arg: "foo:/usr/local/", workspace: "foo", path: "/usr/local/"},
		{arg: "foo:a:b", workspace: "foo", path: "a:b"},
		{arg: "./notes:2024.txt", path: "./notes:2024.txt"},
		{arg: "dir/notes:2024.txt", path: "dir/notes:2024.txt"},
		{arg: `dir\notes:2024.txt`, path: `dir\notes:2024.txt`},
		{arg: ":file", err: true},
		{arg: "c:file", goos: "linux", workspace: "c", path: "file"},
		{arg: `C:\Users\me\file.txt`, goos: "windows", path: `C:\Users\me\file.txt`},
		{arg: "C:file.txt", goos: "windows", path: "C:file.txt"},
		{arg: "foo:file.txt", goos: "windows", workspace: "foo", path: "file.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.arg, func(t *testing.T) {
			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}
			location, err := parseCpLocation(tc.arg, goos)
			if tc.err {
				if err == nil {
					t.Fatalf("expected an error, got %+v", location)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if location.workspace != tc.workspace || location.path != tc.path {
				t.Errorf("expected workspace %q and path %q, got %+v", tc.workspace, tc.path, location)
			}
		})
	}
}

// shellWords returns the words a POSIX shell splits command into, after quote removal and without expansions.
func shellWords(t *testing.T, command string) []string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `eval "set -- $CMD"; for a; do printf '%s\0' "$a"; done`)
	cmd.Env = append(os.Environ(), "CMD="+command)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sh couldn't parse %s: %v", command, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func TestContainerSSHServerCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	workdir := "/workspaces/user's \"project\" $HOME `id` \\"
	server := []string{"/usr/local/bin/devpod", "helper", "ssh-server", "--stdio", "--track-activity", "--workdir", workdir}

	if diff := cmp.Diff(server, shellWords(t, containerSSHServerCommand("root", workdir, false))); diff != "" {
		t.Errorf("unexpected command as root (-want +got):\n%s", diff)
	}

	words := shellWords(t, containerSSHServerCommand("o'brien", workdir, true))
	if len(words) != 4 || words[0] != "su" || words[1] != "-c" || words[3] != "o'brien" {
		t.Fatalf("expected su -c COMMAND USER, got %q", words)
	}
	if diff := cmp.Diff(append(server, "--debug"), shellWords(t, words[2])); diff != "" {
		t.Errorf("unexpected command run by su (-want +got):\n%s", diff)
	}
}
