package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/curityio/devpod/cmd/flags"
	devpodhttp "github.com/curityio/devpod/pkg/http"
	"github.com/curityio/devpod/pkg/workspace"
	"github.com/spf13/cobra"
)

const providerRepoPrefix = "devpod-provider-"

// ListAvailableCmd holds the list cmd flags
type ListAvailableCmd struct {
	flags.GlobalFlags
}

type repoSearchResult struct {
	Items []struct {
		Name string `json:"name"`
	} `json:"items"`
}

func getDevpodProviderList() error {
	url := fmt.Sprintf(
		"https://api.github.com/search/repositories?q=%s+in:name+org:%s&per_page=100",
		providerRepoPrefix,
		workspace.GithubProviderOwner,
	)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := devpodhttp.GetHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("list provider repositories: %s: %s", resp.Status, strings.TrimSpace(string(result)))
	}

	var searchResult repoSearchResult
	err = json.Unmarshal(result, &searchResult)
	if err != nil {
		return err
	}
	repoNames := make([]string, 0, len(searchResult.Items))
	for _, item := range searchResult.Items {
		repoNames = append(repoNames, item.Name)
	}

	fmt.Println("List of available providers:")
	for _, name := range availableProviders(repoNames) {
		fmt.Println("\t", name)
	}

	return nil
}

// availableProviders returns the sorted names of the providers in the given repositories and of the
// providers that are still downloaded from loft-sh.
func availableProviders(repoNames []string) []string {
	names := slices.Clone(workspace.LoftProviders)
	for _, repoName := range repoNames {
		if name, ok := strings.CutPrefix(repoName, providerRepoPrefix); ok && name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// NewListAvailableCmd creates a new command
func NewListAvailableCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListAvailableCmd{
		GlobalFlags: *flags,
	}
	listAvailableCmd := &cobra.Command{
		Use:   "list-available",
		Short: "List providers available for installation",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.Run(context.Background())
		},
	}

	return listAvailableCmd
}

// Run runs the command logic
func (cmd *ListAvailableCmd) Run(ctx context.Context) error {
	return getDevpodProviderList()
}
