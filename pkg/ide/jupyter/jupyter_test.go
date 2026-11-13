package jupyter

import (
	"runtime"
	"testing"

	"github.com/curityio/devpod/pkg/shelltest"
	"gotest.tools/assert"
)

func TestNotebookCommandQuotesWorkspaceFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}

	folder := "/workspaces/" + shelltest.Awkward
	assert.DeepEqual(t, shelltest.Words(t, notebookCommand(folder, 8888)), []string{
		"jupyter", "notebook",
		"--ip=*",
		"--NotebookApp.notebook_dir=" + folder,
		"--NotebookApp.token=",
		"--NotebookApp.password=",
		"--no-browser",
		"--port", "8888",
		"--allow-root",
	})
}
