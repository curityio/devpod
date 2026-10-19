package inject

import (
	"path"
	"strconv"
	"strings"

	"github.com/loft-sh/devpod/pkg/template"
)

func GenerateScript(script string, params *Params) (string, error) {
	rawCode, err := template.FillTemplate(script, map[string]string{
		"Command":         params.Command,
		"ExistsCheck":     params.ExistsCheck,
		"InstallDir":      params.InstallDir(),
		"InstallFilename": params.InstallFilename(),
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
