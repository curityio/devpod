package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// SSHToHTTPSConfigPath is the git config file holding the rewrites, included from the system git config.
const SSHToHTTPSConfigPath = "/var/devpod/ssh-to-https.gitconfig"

// SSHToHTTPSRewrites returns the `url.<base>.insteadOf` rewrites, mapping each ssh prefix to its https base, that
// make git use https for the hosts of the given remote urls and for the given extra hosts. An extra host can also be
// an explicit `from=to` rewrite, which takes precedence over the derived ones.
func SSHToHTTPSRewrites(remoteURLs []string, extraHosts []string) map[string]string {
	rewrites := map[string]string{}
	addHost := func(host string) {
		rewrites["git@"+host+":"] = "https://" + host + "/"
		rewrites["ssh://git@"+host+"/"] = "https://" + host + "/"
	}

	for _, remoteURL := range remoteURLs {
		prefix, host, ok := sshPrefix(remoteURL)
		if !ok {
			continue
		}
		rewrites[prefix] = "https://" + host + "/"
		addHost(host)
	}

	explicit := map[string]string{}
	for _, extraHost := range extraHosts {
		if from, to, ok := strings.Cut(extraHost, "="); ok {
			explicit[strings.TrimSpace(from)] = strings.TrimSpace(to)
		} else {
			addHost(strings.TrimSpace(extraHost))
		}
	}
	for from, to := range explicit {
		rewrites[from] = to
	}

	return rewrites
}

// sshPrefix returns the part of an ssh remote url up to and including the separator before the repository path,
// e.g. `git@github.com:` or `ssh://git@example.com:7999/`, and the host it points to.
func sshPrefix(remoteURL string) (prefix string, host string, ok bool) {
	for _, scheme := range []string{"ssh://", "git+ssh://", "ssh+git://"} {
		rest, found := strings.CutPrefix(remoteURL, scheme)
		if !found {
			continue
		}
		authority, _, found := strings.Cut(rest, "/")
		if !found {
			return "", "", false
		}
		host = authority
		if i := strings.LastIndex(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.HasSuffix(host, "]") {
			host = host[:i]
		}
		if host == "" {
			return "", "", false
		}
		return scheme + authority + "/", host, true
	}

	// scp-like syntax: [user@]host:path, where the host part has no slash
	if strings.Contains(remoteURL, "://") {
		return "", "", false
	}
	authority, _, found := strings.Cut(remoteURL, ":")
	if !found || authority == "" || strings.Contains(authority, "/") {
		return "", "", false
	}
	host = authority
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	// a single letter is a Windows drive, not a host
	if len(host) < 2 {
		return "", "", false
	}
	return authority + ":", host, true
}

// FormatSSHToHTTPSConfig renders the rewrites as a git config file.
func FormatSSHToHTTPSConfig(rewrites map[string]string) string {
	byBase := map[string][]string{}
	for from, to := range rewrites {
		byBase[to] = append(byBase[to], from)
	}
	bases := make([]string, 0, len(byBase))
	for base := range byBase {
		bases = append(bases, base)
	}
	sort.Strings(bases)

	b := &strings.Builder{}
	b.WriteString("# Written by DevPod because GIT_SSH_TO_HTTPS is enabled. Do not edit.\n")
	for _, base := range bases {
		froms := byBase[base]
		sort.Strings(froms)
		fmt.Fprintf(b, "[url %q]\n", base)
		for _, from := range froms {
			fmt.Fprintf(b, "\tinsteadOf = %s\n", from)
		}
	}
	return b.String()
}

// RemoteURLs returns the remote urls of the repository in dir and the urls of its submodules. It reads the config
// files directly, so it works whoever owns the repository.
func RemoteURLs(ctx context.Context, dir string) []string {
	urls := []string{}
	if configPath := repositoryConfigPath(dir); configPath != "" {
		urls = append(urls, configValues(ctx, configPath, `^remote\..*\.url$`)...)
	}
	if gitModules := filepath.Join(dir, ".gitmodules"); fileExists(gitModules) {
		urls = append(urls, configValues(ctx, gitModules, `^submodule\..*\.url$`)...)
	}
	return urls
}

// repositoryConfigPath returns the config file of the repository in dir, following `.git` files of worktrees and
// submodules. It returns an empty string if there is none.
func repositoryConfigPath(dir string) string {
	gitDir := filepath.Join(dir, ".git")
	info, err := os.Stat(gitDir)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		content, err := os.ReadFile(gitDir)
		if err != nil {
			return ""
		}
		path, found := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
		if !found {
			return ""
		}
		gitDir = strings.TrimSpace(path)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(dir, gitDir)
		}
		if commonDir, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
			path := strings.TrimSpace(string(commonDir))
			if !filepath.IsAbs(path) {
				path = filepath.Join(gitDir, path)
			}
			gitDir = path
		}
	}

	configPath := filepath.Join(gitDir, "config")
	if !fileExists(configPath) {
		return ""
	}
	return configPath
}

func configValues(ctx context.Context, file, keyPattern string) []string {
	out, err := CommandContext(ctx, nil, "config", "--file", file, "--get-regexp", keyPattern).Output()
	if err != nil {
		return nil
	}
	values := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if _, value, found := strings.Cut(strings.TrimSpace(line), " "); found {
			values = append(values, strings.TrimSpace(value))
		}
	}
	return values
}

// ConfigureSSHToHTTPS writes the rewrites to SSHToHTTPSConfigPath and includes that file from the system git config.
func ConfigureSSHToHTTPS(ctx context.Context, rewrites map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(SSHToHTTPSConfigPath), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(SSHToHTTPSConfigPath, []byte(FormatSSHToHTTPSConfig(rewrites)), 0644); err != nil {
		return fmt.Errorf("write %s: %w", SSHToHTTPSConfigPath, err)
	}

	out, _ := CommandContext(ctx, nil, "config", "--system", "--get-all", "include.path").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == SSHToHTTPSConfigPath {
			return nil
		}
	}
	out, err := CommandContext(ctx, nil, "config", "--system", "--add", "include.path", SSHToHTTPSConfigPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("include %s in system git config: %s: %w", SSHToHTTPSConfigPath, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RemoveSSHToHTTPS undoes ConfigureSSHToHTTPS.
func RemoveSSHToHTTPS(ctx context.Context) error {
	if !fileExists(SSHToHTTPSConfigPath) {
		return nil
	}

	out, err := CommandContext(ctx, nil, "config", "--system", "--unset-all", "include.path", "^"+regexp.QuoteMeta(SSHToHTTPSConfigPath)+"$").CombinedOutput()
	// exit code 5 means the include was not there
	var exitErr *exec.ExitError
	if err != nil && !(errors.As(err, &exitErr) && exitErr.ExitCode() == 5) {
		return fmt.Errorf("remove include of %s from system git config: %s: %w", SSHToHTTPSConfigPath, strings.TrimSpace(string(out)), err)
	}
	return os.Remove(SSHToHTTPSConfigPath)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
