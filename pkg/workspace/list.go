package workspace

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/loft-sh/devpod/pkg/client"
	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/log"
	providerpkg "github.com/loft-sh/devpod/pkg/provider"
)

func List(ctx context.Context, devPodConfig *config.Config, log log.Logger) ([]*providerpkg.Workspace, error) {
	return ListLocalWorkspaces(devPodConfig.DefaultContext, log)
}

func ListLocalWorkspaces(contextName string, log log.Logger) ([]*providerpkg.Workspace, error) {
	workspaceIDs, err := listWorkspaceIDs(contextName)
	if err != nil {
		return nil, err
	}

	retWorkspaces := []*providerpkg.Workspace{}
	for _, workspaceID := range workspaceIDs {
		workspaceConfig, err := providerpkg.LoadWorkspaceConfig(contextName, workspaceID)
		if err != nil {
			log.ErrorStreamOnly().Warnf("Couldn't load workspace %s: %v", workspaceID, err)
			continue
		}

		retWorkspaces = append(retWorkspaces, workspaceConfig)
	}

	return retWorkspaces, nil
}

// SortByLastUsed sorts workspaces from the most to the least recently used
func SortByLastUsed(workspaces []*providerpkg.Workspace) {
	sort.SliceStable(workspaces, func(i, j int) bool {
		return usedMoreRecently(workspaces[i], workspaces[j])
	})
}

// usedMoreRecently reports whether workspace a was used more recently than b. A nil workspace, one that couldn't
// be loaded, comes last.
func usedMoreRecently(a, b *providerpkg.Workspace) bool {
	if a == nil || b == nil {
		return a != nil && b == nil
	}

	return a.LastUsedTimestamp.Time.After(b.LastUsedTimestamp.Time)
}

// ListedClient is a workspace of the context with its client, or the reason it couldn't be loaded
type ListedClient struct {
	ID string

	// Workspace is nil if the workspace config couldn't be loaded
	Workspace *providerpkg.Workspace
	Client    client.WorkspaceClient
	Err       error
}

// ListClients returns the clients of all workspaces of the context from the most to the least recently used,
// loading the providers only once. Unlike List, it doesn't skip workspaces that can't be loaded, but returns them
// last, with an error.
func ListClients(devPodConfig *config.Config, log log.Logger) ([]ListedClient, error) {
	workspaceIDs, err := listWorkspaceIDs(devPodConfig.DefaultContext)
	if err != nil {
		return nil, err
	}

	var providers map[string]*ProviderWithOptions
	var providersErr error
	if len(workspaceIDs) > 0 {
		providers, providersErr = LoadAllProviders(devPodConfig, log)
	}

	retClients := []ListedClient{}
	for _, workspaceID := range workspaceIDs {
		listed := ListedClient{ID: workspaceID}
		listed.Workspace, listed.Client, listed.Err = loadClient(devPodConfig, workspaceID, providers, providersErr, log)
		retClients = append(retClients, listed)
	}
	sort.SliceStable(retClients, func(i, j int) bool {
		return usedMoreRecently(retClients[i].Workspace, retClients[j].Workspace)
	})

	return retClients, nil
}

func loadClient(
	devPodConfig *config.Config,
	workspaceID string,
	providers map[string]*ProviderWithOptions,
	providersErr error,
	log log.Logger,
) (*providerpkg.Workspace, client.WorkspaceClient, error) {
	workspaceConfig, err := providerpkg.LoadWorkspaceConfig(devPodConfig.DefaultContext, workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("load workspace config: %w", err)
	} else if providersErr != nil {
		return workspaceConfig, nil, providersErr
	}

	providerWithOptions, err := findLoadedProvider(providers, workspaceConfig.Provider.Name)
	if err != nil {
		return workspaceConfig, nil, err
	}

	machineConfig, err := loadWorkspaceMachine(workspaceConfig)
	if err != nil {
		return workspaceConfig, nil, err
	}

	workspaceClient, err := getWorkspaceClient(devPodConfig, providerWithOptions.Config, workspaceConfig, machineConfig, log)
	return workspaceConfig, workspaceClient, err
}

func listWorkspaceIDs(contextName string) ([]string, error) {
	workspaceDir, err := providerpkg.GetWorkspacesDir(contextName)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(workspaceDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	workspaceIDs := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		workspaceIDs = append(workspaceIDs, entry.Name())
	}

	return workspaceIDs, nil
}
