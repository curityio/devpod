package workspace

import (
	"fmt"

	"github.com/loft-sh/devpod/pkg/gitcredentials"
	"github.com/loft-sh/devpod/pkg/log"
	providerpkg "github.com/loft-sh/devpod/pkg/provider"
)

// ConfigureGitUser saves the git identity that the workspace's container gets if it doesn't have
// one yet. A name or email in gitUser replaces the saved one. One that is neither given nor saved
// is the one git uses where the workspace comes from: its local folder, or else dir, the
// directory `devpod up` runs in. That way the repository's config and its includeIf matches
// count, and running `devpod up` again from somewhere else doesn't change the identity.
func ConfigureGitUser(workspace *providerpkg.Workspace, gitUser providerpkg.WorkspaceGitUser, dir string, log log.Logger) error {
	if workspace.Source.LocalFolder != "" {
		dir = workspace.Source.LocalFolder
	}

	newGitUser := resolveGitUser(workspace.GitUser, gitUser, dir)
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

func resolveGitUser(saved, given providerpkg.WorkspaceGitUser, dir string) providerpkg.WorkspaceGitUser {
	gitUser := saved
	if given.Name != "" {
		gitUser.Name = given.Name
	}
	if given.Email != "" {
		gitUser.Email = given.Email
	}

	if gitUser.Name == "" || gitUser.Email == "" {
		hostUser := gitcredentials.GetHostUser(dir)
		if gitUser.Name == "" {
			gitUser.Name = hostUser.Name
		}
		if gitUser.Email == "" {
			gitUser.Email = hostUser.Email
		}
	}

	return gitUser
}
