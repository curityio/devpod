package inject

import (
	"path"
	"strconv"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/curityio/devpod/pkg/template"
)

// GenerateScript fills in the script template. Command and ExistsCheck are shell code and go in as they are; the
// install location is shell-quoted. InstallDirArg and InstallPathArg are quoted twice: the script assigns them to a
// variable, which leaves them quoted once, ready to be put into the command strings the script runs with sh -c.
func GenerateScript(script string, params *Params) (string, error) {
	installDir := params.InstallDir()
	installPath := installDir + "/" + params.InstallFilename()
	rawCode, err := template.FillTemplate(script, map[string]string{
		"Command":         params.Command,
		"ExistsCheck":     params.ExistsCheck,
		"InstallDir":      shellescape.Quote(installDir),
		"InstallFilename": shellescape.Quote(params.InstallFilename()),
		"InstallDirArg":   shellescape.Quote(shellescape.Quote(installDir)),
		"InstallPathArg":  shellescape.Quote(shellescape.Quote(installPath)),
		"ChmodPath":       params.ChmodPath(),
	})
	if err != nil {
		return "", err
	}

	return stripCarriageReturns(rawCode), nil
}

type Params struct {
	Command         string
	AgentRemotePath string

	ExistsCheck     string
	ShouldChmodPath bool
}

func (p *Params) InstallDir() string {
	return path.Dir(p.AgentRemotePath)
}

func (p *Params) InstallFilename() string {
	return path.Base(p.AgentRemotePath)
}

func (p *Params) ChmodPath() string {
	return strconv.FormatBool(p.ShouldChmodPath)
}

func stripCarriageReturns(script string) string {
	return strings.ReplaceAll(script, "\r", "")
}
