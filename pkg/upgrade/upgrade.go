package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/inconshreveable/go-update"
	"github.com/loft-sh/devpod/pkg/log"
	versionpkg "github.com/loft-sh/devpod/pkg/version"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

const (
	repoOwner = "curityio"
	repoName  = "devpod"
)

// releasesURL lists the newest releases. Only the first page is read, which is plenty to find the latest one.
var releasesURL = fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100", repoOwner, repoName)

// Options configures Upgrade.
type Options struct {
	// Version is the release tag to install, with or without the leading "v". If empty, Upgrade installs the
	// newest stable release, but only if it is newer than the running binary.
	Version string
	// DryRun only reports what Upgrade would do, without downloading or replacing anything.
	DryRun bool
}

// Upgrade replaces the running binary with another release of devpod from GitHub.
func Upgrade(ctx context.Context, opts Options, log log.Logger) error {
	current, err := parseVersion(versionpkg.GetVersion())
	if err != nil {
		return fmt.Errorf("cannot tell which version this binary is: %w", err)
	}

	releases, err := listReleases(ctx)
	if err != nil {
		return err
	}

	assetName := cliAssetName(runtime.GOOS, runtime.GOARCH)
	var rel *release
	var asset *releaseAsset
	if opts.Version != "" {
		rel = findRelease(releases, opts.Version)
		if rel == nil {
			return fmt.Errorf("devpod version %s couldn't be found", opts.Version)
		}
		asset = rel.findAsset(assetName)
		if asset == nil {
			return fmt.Errorf("devpod version %s has no CLI for %s/%s", rel.TagName, runtime.GOOS, runtime.GOARCH)
		}
		if strings.TrimPrefix(rel.TagName, "v") == strings.TrimPrefix(versionpkg.GetVersion(), "v") {
			log.Infof("Current binary is already version %s", rel.TagName)
			return nil
		}
	} else {
		var latest version
		rel, asset, latest = latestStableRelease(releases, assetName)
		if rel == nil {
			return fmt.Errorf("no stable release of devpod has a CLI for %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		switch latest.compare(current) {
		case 0:
			log.Infof("Current binary is the latest version: %s", versionpkg.GetVersion())
			return nil
		case -1:
			log.Infof("Current binary (%s) is newer than the latest release (%s)", versionpkg.GetVersion(), rel.TagName)
			return nil
		}
	}

	cmdPath, err := executablePath()
	if err != nil {
		return err
	}
	permErr := checkCanReplace(cmdPath)

	if opts.DryRun {
		if opts.Version != "" {
			log.Infof("Would install devpod version %s (current version: %s)", rel.TagName, versionpkg.GetVersion())
		} else {
			log.Infof("A newer version of devpod is available: %s (current version: %s). Run `devpod upgrade` to install it.", rel.TagName, versionpkg.GetVersion())
		}
		if permErr != nil {
			log.Warnf("The upgrade would fail: %v", permErr)
		}
		return nil
	}
	if permErr != nil {
		return permErr
	}

	updater, err := selfupdate.NewUpdater(selfupdate.Config{APIToken: os.Getenv("GITHUB_TOKEN")})
	if err != nil {
		return fmt.Errorf("failed to initialize updater: %w", err)
	}

	log.Infof("Downloading devpod %s...", rel.TagName)
	err = updater.UpdateTo(&selfupdate.Release{
		AssetURL:      asset.BrowserDownloadURL,
		AssetByteSize: asset.Size,
		AssetID:       asset.ID,
		RepoOwner:     repoOwner,
		RepoName:      repoName,
	}, cmdPath)
	if err != nil {
		return err
	}

	log.Donef("Successfully updated devpod to version %s", rel.TagName)
	if opts.Version == "" && rel.Body != "" {
		log.Infof("Release notes: \n\n%s", rel.Body)
	}
	return nil
}

// version is a devpod version: at least three numbers, optionally followed by a suffix such as "-alpha.1".
type version struct {
	numbers []int
	suffix  string
}

var versionRegex = regexp.MustCompile(`^v?(\d+(?:\.\d+){2,})(.*)$`)

func parseVersion(s string) (version, error) {
	m := versionRegex.FindStringSubmatch(s)
	if m == nil {
		return version{}, fmt.Errorf("version %q does not start with three numbers", s)
	}

	parts := strings.Split(m[1], ".")
	numbers := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil {
			return version{}, fmt.Errorf("version %q: %w", s, err)
		}
		numbers[i] = n
	}

	return version{numbers: numbers, suffix: m[2]}, nil
}

// isStable reports whether v is a plain major.minor.patch version, the only kind Upgrade installs on its own.
func (v version) isStable() bool {
	return len(v.numbers) == 3 && v.suffix == ""
}

// compare returns -1, 0 or 1 when v is older than, as old as, or newer than o. Numbers are compared in order,
// with missing ones counting as 0. When they are equal, a version with a suffix (a pre-release or development
// build) is older than one without, and two suffixes are not ordered.
func (v version) compare(o version) int {
	for i := 0; i < len(v.numbers) || i < len(o.numbers); i++ {
		a, b := v.number(i), o.number(i)
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}

	switch {
	case v.suffix == "" && o.suffix != "":
		return 1
	case v.suffix != "" && o.suffix == "":
		return -1
	}
	return 0
}

func (v version) number(i int) int {
	if i < len(v.numbers) {
		return v.numbers[i]
	}
	return 0
}

type release struct {
	TagName    string         `json:"tag_name"`
	Body       string         `json:"body"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int    `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (r *release) findAsset(name string) *releaseAsset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// cliAssetName is the name the release workflow gives the CLI binary for a platform.
func cliAssetName(goos, goarch string) string {
	name := fmt.Sprintf("devpod-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// latestStableRelease returns the release with the highest stable version that has the given asset, ignoring
// drafts, pre-releases and tags that aren't plain major.minor.patch versions.
func latestStableRelease(releases []release, assetName string) (*release, *releaseAsset, version) {
	var latest *release
	var latestAsset *releaseAsset
	var latestVersion version
	for i := range releases {
		rel := &releases[i]
		if rel.Draft || rel.Prerelease {
			continue
		}
		v, err := parseVersion(rel.TagName)
		if err != nil || !v.isStable() {
			continue
		}
		asset := rel.findAsset(assetName)
		if asset == nil {
			continue
		}
		if latest == nil || v.compare(latestVersion) > 0 {
			latest, latestAsset, latestVersion = rel, asset, v
		}
	}
	return latest, latestAsset, latestVersion
}

// findRelease returns the release tagged tag, accepting the tag with or without its leading "v".
func findRelease(releases []release, tag string) *release {
	for i := range releases {
		if t := releases[i].TagName; t == tag || strings.TrimPrefix(t, "v") == strings.TrimPrefix(tag, "v") {
			return &releases[i]
		}
	}
	return nil
}

func listReleases(ctx context.Context) ([]release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list devpod releases: %w", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("list devpod releases: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &apiErr)
		err := fmt.Errorf("list devpod releases: GitHub returned %s: %s", res.Status, apiErr.Message)
		if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests {
			err = fmt.Errorf("%w (set GITHUB_TOKEN to raise GitHub's rate limit)", err)
		}
		return nil, err
	}

	var releases []release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("parse devpod releases: %w", err)
	}
	return releases, nil
}

// executablePath returns the path of the running binary, following symlinks so that an upgrade replaces the
// binary itself rather than a link to it.
func executablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the devpod binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve the devpod binary %s: %w", path, err)
	}
	return resolved, nil
}

// checkCanReplace fails if the current user can't replace the binary at path. The update writes the new binary
// next to the old one and renames it into place, so it needs to create files in the binary's directory.
func checkCanReplace(path string) error {
	opts := update.Options{TargetPath: path, TargetMode: 0755}
	if err := opts.CheckPermissions(); err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			err = pathErr.Err
		}
		return fmt.Errorf("the current user can't replace %s, as it can't create files in %s: %w", path, filepath.Dir(path), err)
	}
	return nil
}
