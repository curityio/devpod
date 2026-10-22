package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loft-sh/devpod/pkg/download"
)

const (
	repoOwner = "curityio"
	repoName  = "devpod"
)

// apiURL is GitHub's REST API. It is a variable so that tests can replace it.
var apiURL = "https://api.github.com"

// githubGet sends a GET request to the GitHub API. It uses GITHUB_TOKEN if it's set, which raises GitHub's rate
// limit. Requests here don't use pkg/http's client, because that one doesn't verify TLS certificates.
func githubGet(ctx context.Context, path, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return http.DefaultClient.Do(req)
}

// githubError describes a failed GitHub API response.
func githubError(what string, res *http.Response) error {
	var apiErr struct {
		Message string `json:"message"`
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	_ = json.Unmarshal(body, &apiErr)
	err := fmt.Errorf("%s: GitHub returned %s: %s", what, res.Status, apiErr.Message)
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests {
		err = fmt.Errorf("%w (set GITHUB_TOKEN to raise GitHub's rate limit)", err)
	}
	return err
}

// getJSON decodes a GitHub API response into out. It returns false if GitHub answered 404.
func getJSON(ctx context.Context, what, path string, out any) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	res, err := githubGet(ctx, path, "application/vnd.github+json")
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if res.StatusCode != http.StatusOK {
		return false, githubError(what, res)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	return true, nil
}

// listReleases returns the newest releases. Only the first page is read, which is plenty to find the latest one.
func listReleases(ctx context.Context) ([]download.GithubRelease, error) {
	var releases []download.GithubRelease
	found, err := getJSON(ctx, "list devpod releases", fmt.Sprintf("/repos/%s/%s/releases?per_page=100", repoOwner, repoName), &releases)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("list devpod releases: GitHub has no repository %s/%s", repoOwner, repoName)
	}
	return releases, nil
}

// latestRelease returns the release GitHub considers the latest (the newest that is neither a draft nor a
// pre-release), or no release if there is none.
func latestRelease(ctx context.Context) (*download.GithubRelease, error) {
	rel := &download.GithubRelease{}
	found, err := getJSON(ctx, "find the latest devpod release", fmt.Sprintf("/repos/%s/%s/releases/latest", repoOwner, repoName), rel)
	if err != nil || !found {
		return nil, err
	}
	return rel, nil
}

// releaseByTag returns the release tagged tag, trying the tag with and without its leading "v". It returns no
// release if there is none.
func releaseByTag(ctx context.Context, tag string) (*download.GithubRelease, error) {
	tags := []string{tag}
	if strings.HasPrefix(tag, "v") {
		tags = append(tags, strings.TrimPrefix(tag, "v"))
	} else {
		tags = append(tags, "v"+tag)
	}

	for _, t := range tags {
		rel := &download.GithubRelease{}
		found, err := getJSON(ctx, "find devpod release "+t, fmt.Sprintf("/repos/%s/%s/releases/tags/%s", repoOwner, repoName, url.PathEscape(t)), rel)
		if err != nil {
			return nil, err
		}
		if found {
			return rel, nil
		}
	}
	return nil, nil
}

// downloadPattern names the hidden files releases are downloaded to.
const downloadPattern = ".devpod-download-*"

// downloadAsset downloads a release asset into a new hidden file in dir and returns its path. It removes the file
// if the download fails or ctx is cancelled, and files that interrupted downloads left behind.
func downloadAsset(ctx context.Context, asset *download.GithubReleaseAsset, dir string) (string, error) {
	res, err := githubGet(ctx, fmt.Sprintf("/repos/%s/%s/releases/assets/%d", repoOwner, repoName, asset.ID), "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", githubError("download "+asset.Name, res)
	}

	removeStaleDownloads(dir)
	f, err := os.CreateTemp(dir, downloadPattern)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, res.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && asset.Size > 0 && n != asset.Size {
		err = fmt.Errorf("got %d bytes, expected %d", n, asset.Size)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("download %s: %w", asset.Name, err)
	}
	return f.Name(), nil
}

// removeStaleDownloads removes download files in dir that haven't been written to for an hour, which a killed
// upgrade left behind. Newer ones may belong to an upgrade that is still running.
func removeStaleDownloads(dir string) {
	paths, err := filepath.Glob(filepath.Join(dir, downloadPattern))
	if err != nil {
		return
	}
	for _, path := range paths {
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(path)
		}
	}
}
