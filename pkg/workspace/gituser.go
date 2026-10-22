package workspace

import (
	"fmt"

	"github.com/loft-sh/devpod/pkg/gitcredentials"
	"github.com/loft-sh/devpod/pkg/log"
	providerpkg "github.com/loft-sh/devpod/pkg/provider"
)

// GitUserOptions are the `devpod up` options for the git identity of the workspace's container.
type GitUserOptions struct {
	// Name and Email replace the saved values unless they are nil. An empty value removes the
	// saved one, so that DevPod looks it up again.
	Name  *string
	Email *string

	// Dir is the directory `devpod up` runs in
	Dir string
}

// ConfigureGitUser saves the git identity that the workspace's container gets if it doesn't have
// one yet. A name or email in options replaces the saved one. One that is neither given nor saved
// is the one git uses in the repository the workspace comes from: its local folder, or else the
// directory `devpod up` runs in. That way the repository's config and its includeIf matches
// count, and running `devpod up` again from somewhere else doesn't change the identity. Outside a
// repository nothing is saved, so the container gets the global identity when it connects.
func ConfigureGitUser(workspace *providerpkg.Workspace, options GitUserOptions, log log.Logger) error {
	dir := options.Dir
	if workspace.Source.LocalFolder != "" {
		dir = workspace.Source.LocalFolder
	}

	newGitUser := resolveGitUser(workspace.GitUser, options, dir, log)
	if newGitUser == workspace.GitUser {
		return nil
	}

	log.Debugf("Use git identity '%s <%s>' for workspace %s", newGitUser.Name, newGitUser.Email, workspace.ID)
	workspace.GitUser = newGitUser
	err := providerpkg.SaveWorkspaceConfig(workspace)
	if err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}

	return nil
}

func resolveGitUser(saved providerpkg.WorkspaceGitUser, options GitUserOptions, dir string, log log.Logger) providerpkg.WorkspaceGitUser {
	gitUser := saved
	if options.Name != nil {
		gitUser.Name = *options.Name
	}
	if options.Email != nil {
		gitUser.Email = *options.Email
	}

	if gitUser.Name == "" || gitUser.Email == "" {
		repositoryUser, err := gitcredentials.GetRepositoryUser(dir)
		if err != nil {
			log.Debugf("Not using the git identity of %q: %v", dir, err)
			return gitUser
		}

		if gitUser.Name == "" {
			gitUser.Name = repositoryUser.Name
		}
		if gitUser.Email == "" {
			gitUser.Email = repositoryUser.Email
		}
	}

	return gitUser
}
