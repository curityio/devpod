package upgrade

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/loft-sh/devpod/pkg/download"
	"github.com/loft-sh/devpod/pkg/log"
	versionpkg "github.com/loft-sh/devpod/pkg/version"
)

// These are variables so that tests can replace them.
var (
	currentVersion = versionpkg.GetVersion
	findBinary     = runningBinary
)

// Options configures Upgrade.
type Options struct {
	// Version is the release tag to install, with or without the leading "v". If empty, Upgrade installs the
	// newest stable release, but only if it is newer than the running binary.
	Version string
	// DryRun only reports what Upgrade would do, without downloading or replacing anything.
	DryRun bool
}

// Upgrade installs another release of devpod from GitHub in place of the running binary. If the binary was started
// through a symlink, Upgrade downloads the release next to the file the link points to and points the link to it
// instead, leaving the previous binary alone.
func Upgrade(ctx context.Context, opts Options, log log.Logger) error {
	rel, asset, err := pickRelease(ctx, opts, log)
	if err != nil || rel == nil {
		return err
	}

	binary, err := findBinary()
	if err != nil {
		return err
	}
	in, err := planInstall(binary, rel.TagName)
	if err != nil {
		return err
	}
	permErr := in.checkPermissions()

	if opts.DryRun {
		if opts.Version != "" {
			log.Infof("Would install devpod %s (current version: %s)", rel.TagName, currentVersion())
		} else {
			log.Infof("A newer version of devpod is available: %s (current version: %s)", rel.TagName, currentVersion())
		}
		if in.link == "" {
			log.Infof("`devpod upgrade` would replace %s", in.binary)
		} else {
			log.Infof("`devpod upgrade` would download it to %s and point %s to it, leaving %s alone", in.binary, in.link, in.previous)
		}
		if in.overwrites {
			log.Warnf("%s already exists and would be replaced", in.binary)
		}
		if permErr != nil {
			return fmt.Errorf("the upgrade would fail: %w", permErr)
		}
		return nil
	}
	if permErr != nil {
		return permErr
	}

	if in.overwrites {
		log.Warnf("Replacing %s, which already exists", in.binary)
	}

	removeStaleFiles(in.dirs())
	log.Infof("Downloading devpod %s...", rel.TagName)
	downloaded, err := downloadAsset(ctx, asset, in.dir())
	if err != nil {
		return err
	}
	if err := in.apply(downloaded, log); err != nil {
		_ = os.Remove(downloaded)
		return err
	}

	if in.link == "" {
		log.Donef("Successfully updated devpod to version %s", rel.TagName)
	} else {
		log.Donef("Downloaded devpod %s to %s and pointed %s to it", rel.TagName, in.binary, in.link)
		log.Infof("The previous binary, %s, was left alone", in.previous)
	}
	if opts.Version == "" && rel.Body != "" {
		log.Infof("Release notes: \n\n%s", rel.Body)
	}
	return nil
}

// pickRelease returns the release to install and its CLI asset, or no release if there is nothing to do.
func pickRelease(ctx context.Context, opts Options, log log.Logger) (*download.GithubRelease, *download.GithubReleaseAsset, error) {
	assetName := cliAssetName(runtime.GOOS, runtime.GOARCH)

	if opts.Version != "" {
		rel, err := releaseByTag(ctx, opts.Version)
		if err != nil {
			return nil, nil, err
		}
		if rel == nil {
			return nil, nil, fmt.Errorf("devpod version %s couldn't be found", opts.Version)
		}
		asset := findAsset(rel, assetName)
		if asset == nil {
			return nil, nil, fmt.Errorf("devpod version %s has no CLI for %s/%s", rel.TagName, runtime.GOOS, runtime.GOARCH)
		}
		if sameTag(rel.TagName, currentVersion()) {
			log.Infof("Current binary is already version %s", rel.TagName)
			return nil, nil, nil
		}
		return rel, asset, nil
	}

	current, err := parseVersion(currentVersion())
	if err != nil {
		return nil, nil, fmt.Errorf("cannot tell which version this binary is, use --version to pick one: %w", err)
	}
	releases, err := listReleases(ctx)
	if err != nil {
		return nil, nil, err
	}
	rel, asset, latest := latestStableRelease(releases, assetName)
	if rel == nil {
		// GitHub sometimes answers the list with no releases at all, so also ask for the latest one directly.
		latestRel, err := latestRelease(ctx)
		if err != nil {
			return nil, nil, err
		}
		if latestRel != nil {
			rel, asset, latest = latestStableRelease([]download.GithubRelease{*latestRel}, assetName)
		}
	}
	if rel == nil {
		return nil, nil, fmt.Errorf("no stable release of devpod has a CLI for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	switch latest.compare(current) {
	case 0:
		log.Infof("Current binary is the latest version: %s", currentVersion())
		return nil, nil, nil
	case -1:
		log.Infof("Current binary (%s) is newer than the latest release (%s)", currentVersion(), rel.TagName)
		return nil, nil, nil
	}
	return rel, asset, nil
}

// version is a devpod version: at least three numbers, optionally followed by a suffix such as "-alpha.1" and by
// build metadata such as "+abc123".
type version struct {
	numbers []int
	suffix  string
	build   string
}

var versionRegex = regexp.MustCompile(`^v?(\d+(?:\.\d+){2,})([^+]*)(?:\+(.*))?$`)

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

	return version{numbers: numbers, suffix: m[2], build: m[3]}, nil
}

// isStable reports whether v is a plain major.minor.patch version, the only kind Upgrade installs on its own.
func (v version) isStable() bool {
	return len(v.numbers) == 3 && v.suffix == "" && v.build == ""
}

// compare returns -1, 0 or 1 when v is older than, as old as, or newer than o. Numbers are compared in order,
// with missing ones counting as 0. When they are equal, a version with a suffix (a pre-release or development
// build) is older than one without, and two suffixes are not ordered. Build metadata is ignored.
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

// sameTag reports whether two release tags are the same, with or without their leading "v".
func sameTag(a, b string) bool {
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}

// cliAssetName is the name the release workflow gives the CLI binary for a platform.
func cliAssetName(goos, goarch string) string {
	name := fmt.Sprintf("devpod-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func findAsset(rel *download.GithubRelease, name string) *download.GithubReleaseAsset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// latestStableRelease returns the release with the highest stable version that has the given asset, ignoring
// drafts, pre-releases and tags that aren't plain major.minor.patch versions.
func latestStableRelease(releases []download.GithubRelease, assetName string) (*download.GithubRelease, *download.GithubReleaseAsset, version) {
	var latest *download.GithubRelease
	var latestAsset *download.GithubReleaseAsset
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
		asset := findAsset(rel, assetName)
		if asset == nil {
			continue
		}
		if latest == nil || v.compare(latestVersion) > 0 {
			latest, latestAsset, latestVersion = rel, asset, v
		}
	}
	return latest, latestAsset, latestVersion
}
