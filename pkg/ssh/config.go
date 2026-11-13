package ssh

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/curityio/devpod/pkg/log"
	"github.com/curityio/devpod/pkg/log/scanner"
	"github.com/curityio/devpod/pkg/util"
	"github.com/pkg/errors"
)

var configLock sync.Mutex

var (
	MarkerStartPrefix = "# DevPod Start "
	MarkerEndPrefix   = "# DevPod End "
)

func ConfigureSSHConfig(sshConfigPath, context, workspace, user, workdir string, gpgagent, forwardAgent bool, devPodHome string, log log.Logger) error {
	configLock.Lock()
	defer configLock.Unlock()

	newFile, err := addHost(sshConfigPath, workspace+"."+"devpod", user, context, workspace, workdir, gpgagent, forwardAgent, devPodHome)
	if err != nil {
		return errors.Wrap(err, "parse ssh config")
	}

	return writeSSHConfig(sshConfigPath, newFile, log)
}

type DevPodSSHEntry struct {
	Host      string
	User      string
	Workspace string
}

func addHost(path, host, user, context, workspace, workdir string, gpgagent, forwardAgent bool, devPodHome string) (string, error) {
	newConfig, err := removeFromConfig(path, host)
	if err != nil {
		return "", err
	}

	// get path to executable
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}

	return addHostSection(runtime.GOOS, newConfig, execPath, host, user, context, workspace, workdir, gpgagent, forwardAgent, devPodHome)
}

// addHostSection adds the host's section to config, for OpenSSH on goos.
func addHostSection(goos, config, execPath, host, user, context, workspace, workdir string, gpgagent, forwardAgent bool, devPodHome string) (string, error) {
	for _, value := range []struct{ name, value string }{
		{"devpod executable path", execPath},
		{"host", host},
		{"user", user},
		{"context", context},
		{"workspace", workspace},
		{"workdir", workdir},
		{"devpod home", devPodHome},
	} {
		if err := checkSSHConfigValue(value.name, value.value); err != nil {
			return config, err
		}
	}

	newLines := []string{}
	// add new section
	startMarker := MarkerStartPrefix + host
	endMarker := MarkerEndPrefix + host
	newLines = append(newLines, startMarker)
	newLines = append(newLines, "Host "+host)
	if forwardAgent {
		newLines = append(newLines, "  ForwardAgent yes")
	} else {
		newLines = append(newLines, "  ForwardAgent no")
	}
	newLines = append(newLines, "  LogLevel error")
	newLines = append(newLines, "  StrictHostKeyChecking no")
	newLines = append(newLines, "  UserKnownHostsFile /dev/null")
	newLines = append(newLines, "  HostKeyAlgorithms rsa-sha2-256,rsa-sha2-512,ssh-rsa")

	args := []string{execPath, "ssh", "--stdio", "--context", context, "--user", user, workspace}
	if devPodHome != "" {
		args = append(args, "--devpod-home", devPodHome)
	}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	if gpgagent {
		args = append(args, "--gpg-agent-forwarding")
	}
	newLines = append(newLines, "  ProxyCommand "+proxyCommandLine(goos, args...))
	newLines = append(newLines, "  User "+user)
	newLines = append(newLines, endMarker)

	// now we append the original config
	// keep our blocks on top of the hosts for priority reasons, but below any includes
	lineNumber := 0
	found := false
	lines := []string{}
	commentLines := 0
	scanner := bufio.NewScanner(strings.NewReader(config))
	for scanner.Scan() {
		line := scanner.Text()
		// Check `Host` keyword
		if strings.HasPrefix(strings.TrimSpace(line), "Host") && !found {
			found = true
			lineNumber = max(lineNumber-commentLines, 0)
		}

		// Preserve comments
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			commentLines++
		} else {
			commentLines = 0
		}

		if !found {
			lineNumber++
		}

		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return config, err
	}

	lines = slices.Insert(lines, lineNumber, newLines...)

	newLineSep := "\n"
	if goos == "windows" {
		newLineSep = "\r\n"
	}

	return strings.Join(lines, newLineSep), nil
}

func GetUser(workspaceID string, sshConfigPath string) (string, error) {
	path, err := ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return "", errors.Wrap(err, "Invalid ssh config path")
	}
	sshConfigPath = path

	user := "root"
	_, err = transformHostSection(sshConfigPath, workspaceID+"."+"devpod", func(line string) string {
		splitted := strings.Split(strings.ToLower(strings.TrimSpace(line)), " ")
		if len(splitted) == 2 && splitted[0] == "user" {
			user = strings.Trim(splitted[1], "\"")
		}

		return line
	})
	if err != nil {
		return "", err
	}

	return user, nil
}

// GetWorkdir returns the folder DevPod's SSH config entry for the workspace opens, which `devpod up` sets to the
// workspace folder in the container. It returns "" if the entry doesn't set one.
func GetWorkdir(workspaceID string, sshConfigPath string) (string, error) {
	path, err := ResolveSSHConfigPath(sshConfigPath)
	if err != nil {
		return "", errors.Wrap(err, "Invalid ssh config path")
	}

	workdir := ""
	_, err = transformHostSection(path, workspaceID+"."+"devpod", func(line string) string {
		if strings.HasPrefix(strings.TrimSpace(line), "ProxyCommand ") {
			workdir = parseWorkdirFlag(runtime.GOOS, line)
		}
		return line
	})
	if err != nil {
		return "", err
	}

	return workdir, nil
}

// parseWorkdirFlag returns the value of the --workdir that addHostSection adds to the ProxyCommand line, written
// for goos.
func parseWorkdirFlag(goos, line string) string {
	value, found := strings.CutPrefix(strings.TrimSpace(line), "ProxyCommand ")
	if !found {
		return ""
	}
	args := splitProxyCommandLine(goos, value)
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--workdir":
			if i+1 < len(args) {
				return args[i+1]
			}
		case "--context", "--user", "--devpod-home":
			// skip the flag's value, which can be "--workdir" itself
			i++
		}
	}
	return ""
}

func RemoveFromConfig(workspaceID string, sshConfigPath string, log log.Logger) error {
	configLock.Lock()
	defer configLock.Unlock()

	newFile, err := removeFromConfig(sshConfigPath, workspaceID+"."+"devpod")
	if err != nil {
		return errors.Wrap(err, "parse ssh config")
	}

	return writeSSHConfig(sshConfigPath, newFile, log)
}

func writeSSHConfig(path, content string, log log.Logger) error {
	err := os.MkdirAll(filepath.Dir(path), 0755)
	if err != nil {
		log.Debugf("error creating ssh directory: %v", err)
	}

	err = os.WriteFile(path, []byte(content), 0600)
	if err != nil {
		return errors.Wrap(err, "write ssh config")
	}

	return nil
}

func ResolveSSHConfigPath(sshConfigPath string) (string, error) {
	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "get home dir")
	}

	if sshConfigPath == "" {
		return filepath.Join(homeDir, ".ssh", "config"), nil
	}

	if strings.HasPrefix(sshConfigPath, "~/") {
		sshConfigPath = strings.Replace(sshConfigPath, "~", homeDir, 1)
	}

	return filepath.Abs(sshConfigPath)
}

func removeFromConfig(path, host string) (string, error) {
	return transformHostSection(path, host, func(line string) string {
		return ""
	})
}

func transformHostSection(path, host string, transform func(line string) string) (string, error) {
	var reader io.Reader
	f, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}

		reader = strings.NewReader("")
	} else {
		reader = f
		defer f.Close()
	}

	configScanner := scanner.NewScanner(reader)
	newLines := []string{}
	inSection := false
	startMarker := MarkerStartPrefix + host
	endMarker := MarkerEndPrefix + host
	for configScanner.Scan() {
		text := configScanner.Text()
		if strings.HasPrefix(text, startMarker) {
			inSection = true
		} else if strings.HasPrefix(text, endMarker) {
			inSection = false
		} else if !inSection {
			newLines = append(newLines, text)
		} else if inSection {
			text = transform(text)
			if text != "" {
				newLines = append(newLines, text)
			}
		}
	}
	if configScanner.Err() != nil {
		return "", errors.Wrap(err, "parse ssh config")
	}

	// remove residual empty line at start file
	if len(newLines) > 0 && newLines[0] == "" {
		newLines = newLines[1:]
	}

	return strings.Join(newLines, "\n"), nil
}
