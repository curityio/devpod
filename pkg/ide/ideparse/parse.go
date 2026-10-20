package ideparse

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/loft-sh/devpod/pkg/command"
	"github.com/loft-sh/devpod/pkg/config"
	"github.com/loft-sh/devpod/pkg/ide"
	"github.com/loft-sh/devpod/pkg/ide/jetbrains"
	"github.com/loft-sh/devpod/pkg/ide/jupyter"
	"github.com/loft-sh/devpod/pkg/ide/openvscode"
	"github.com/loft-sh/devpod/pkg/ide/rstudio"
	"github.com/loft-sh/devpod/pkg/ide/vscode"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/provider"
	"github.com/pkg/errors"
)

type AllowedIDE struct {
	// Name of the IDE
	Name config.IDE `json:"name,omitempty"`
	// DisplayName is the name to show to the user
	DisplayName string `json:"displayName,omitempty"`
	// Options of the IDE
	Options ide.Options `json:"options,omitempty"`
	// Icon holds an image URL that will be displayed
	Icon string `json:"icon,omitempty"`
	// IconDark holds an image URL that will be displayed in dark mode
	IconDark string `json:"iconDark,omitempty"`
	// Experimental indicates that this IDE is experimental
	Experimental bool `json:"experimental,omitempty"`
	// Group this IDE belongs to, e.g. for navigation
	Group config.IDEGroup `json:"group,omitempty"`
}

var AllowedIDEs = []AllowedIDE{
	{
		Name:        config.IDENone,
		DisplayName: "None",
		Options:     map[string]ide.Option{},
		Icon:        "https://devpod.curityio.net/icons/none.svg",
		IconDark:    "https://devpod.curityio.net/icons/none_dark.svg",
		Group:       config.IDEGroupPrimary,
	},
	{
		Name:        config.IDEVSCode,
		DisplayName: "VSCode",
		Options:     vscode.Options,
		Icon:        "https://devpod.curityio.net/icons/vscode.svg",
		Group:       config.IDEGroupPrimary,
	},
	{
		Name:        config.IDEOpenVSCode,
		DisplayName: "VSCode Browser",
		Options:     openvscode.Options,
		Icon:        "https://devpod.curityio.net/icons/vscodebrowser.svg",
		Group:       config.IDEGroupPrimary,
	},
	{
		Name:         config.IDECursor,
		DisplayName:  "Cursor",
		Options:      vscode.Options,
		Icon:         "https://devpod.curityio.net/icons/cursor.svg",
		Experimental: true,
		Group:        config.IDEGroupPrimary,
	},
	{
		Name:         config.IDEZed,
		DisplayName:  "Zed",
		Options:      ide.Options{},
		Icon:         "https://devpod.curityio.net/icons/zed.svg",
		Experimental: true,
		Group:        config.IDEGroupPrimary,
	},
	{
		Name:         config.IDECodium,
		DisplayName:  "Codium",
		Options:      vscode.Options,
		Icon:         "https://devpod.curityio.net/icons/codium.svg",
		Experimental: true,
		Group:        config.IDEGroupPrimary,
	},
	{
		Name:        config.IDEIntellij,
		DisplayName: "Intellij",
		Options:     jetbrains.IntellijOptions,
		Icon:        "https://devpod.curityio.net/icons/intellij.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDEPyCharm,
		DisplayName: "PyCharm",
		Options:     jetbrains.PyCharmOptions,
		Icon:        "https://devpod.curityio.net/icons/pycharm.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDEPhpStorm,
		DisplayName: "PhpStorm",
		Options:     jetbrains.PhpStormOptions,
		Icon:        "https://devpod.curityio.net/icons/phpstorm.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDERider,
		DisplayName: "Rider",
		Options:     jetbrains.RiderOptions,
		Icon:        "https://devpod.curityio.net/icons/rider.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDEGoland,
		DisplayName: "Goland",
		Options:     jetbrains.GolandOptions,
		Icon:        "https://devpod.curityio.net/icons/goland.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDEWebStorm,
		DisplayName: "WebStorm",
		Options:     jetbrains.WebStormOptions,
		Icon:        "https://devpod.curityio.net/icons/webstorm.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDERustRover,
		DisplayName: "RustRover",
		Options:     jetbrains.RustRoverOptions,
		Icon:        "https://devpod.curityio.net/icons/rustrover.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDERubyMine,
		DisplayName: "RubyMine",
		Options:     jetbrains.RubyMineOptions,
		Icon:        "https://devpod.curityio.net/icons/rubymine.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDECLion,
		DisplayName: "CLion",
		Options:     jetbrains.CLionOptions,
		Icon:        "https://devpod.curityio.net/icons/clion.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:        config.IDEDataSpell,
		DisplayName: "DataSpell",
		Options:     jetbrains.DataSpellOptions,
		Icon:        "https://devpod.curityio.net/icons/dataspell.svg",
		Group:       config.IDEGroupJetBrains,
	},
	{
		Name:         config.IDEJupyterNotebook,
		DisplayName:  "Jupyter Notebook",
		Options:      jupyter.Options,
		Icon:         "https://devpod.curityio.net/icons/jupyter.svg",
		IconDark:     "https://devpod.curityio.net/icons/jupyter_dark.svg",
		Experimental: true,
		Group:        config.IDEGroupOther,
	},
	{
		Name:         config.IDEVSCodeInsiders,
		DisplayName:  "VSCode Insiders",
		Options:      vscode.Options,
		Icon:         "https://devpod.curityio.net/icons/vscode_insiders.svg",
		Experimental: true,
		Group:        config.IDEGroupOther,
	},
	{
		Name:         config.IDEPositron,
		DisplayName:  "Positron",
		Options:      vscode.Options,
		Icon:         "https://devpod.curityio.net/icons/positron.svg",
		Experimental: true,
		Group:        config.IDEGroupOther,
	},
	{
		Name:         config.IDERStudio,
		DisplayName:  "RStudio Server",
		Options:      rstudio.Options,
		Icon:         "https://devpod.curityio.net/icons/rstudio.svg",
		Experimental: true,
		Group:        config.IDEGroupOther,
	},
	{
		Name:         config.IDEWindsurf,
		DisplayName:  "Windsurf",
		Options:      vscode.Options,
		Icon:         "https://devpod.curityio.net/icons/windsurf.svg",
		Experimental: true,
		Group:        config.IDEGroupPrimary,
	},
}

// discontinuedIDEs are IDEs that were once supported and may still be stored
// in existing workspaces or as the default IDE, but are no longer available.
var discontinuedIDEs = map[string]string{
	"fleet": "JetBrains discontinued Fleet",
}

func RefreshIDEOptions(devPodConfig *config.Config, workspace *provider.Workspace, ide string, options []string, log log.Logger) (*provider.Workspace, error) {
	ide = strings.ToLower(ide)
	if ide == "" {
		if workspace.IDE.Name != "" {
			ide = workspace.IDE.Name
		} else if devPodConfig.Current().DefaultIDE != "" {
			ide = devPodConfig.Current().DefaultIDE
		} else {
			ide = detect()
		}

		// a stored IDE that is no longer supported falls back to none
		if reason, ok := discontinuedIDEs[strings.ToLower(ide)]; ok {
			log.Warnf("IDE '%s' is no longer supported (%s), using '%s' instead. Use --ide to choose another IDE", ide, reason, config.IDENone)
			ide = string(config.IDENone)
		}
	}

	// get ide options
	ideOptions, err := GetIDEOptions(ide)
	if err != nil {
		return nil, err
	}

	// get global options and set them as non user
	// provided.
	retValues := devPodConfig.IDEOptions(ide)
	for k, v := range retValues {
		retValues[k] = config.OptionValue{
			Value: v.Value,
		}
	}

	// get existing options
	if ide == workspace.IDE.Name {
		for k, v := range workspace.IDE.Options {
			if !v.UserProvided {
				continue
			}

			retValues[k] = v
		}
	}

	// get user options
	values, err := ParseOptions(options, ideOptions)
	if err != nil {
		return nil, errors.Wrap(err, "parse options")
	}
	for k, v := range values {
		retValues[k] = v
	}

	// check if we need to modify workspace
	if workspace.IDE.Name != ide || !reflect.DeepEqual(workspace.IDE.Options, retValues) {
		workspace.IDE.Name = ide
		workspace.IDE.Options = retValues
		err = provider.SaveWorkspaceConfig(workspace)
		if err != nil {
			return nil, errors.Wrap(err, "save workspace")
		}
	}

	return workspace, nil
}

func GetIDEOptions(ide string) (ide.Options, error) {
	var match *AllowedIDE
	for _, m := range AllowedIDEs {
		m := m
		if string(m.Name) == ide {
			match = &m
			break
		}
	}
	if match == nil {
		allowedIDEArray := []string{}
		for _, a := range AllowedIDEs {
			allowedIDEArray = append(allowedIDEArray, string(a.Name))
		}

		return nil, fmt.Errorf("unrecognized ide '%s', please use one of: %v", ide, allowedIDEArray)
	}

	return match.Options, nil
}

func ParseOptions(options []string, ideOptions ide.Options) (map[string]config.OptionValue, error) {
	if ideOptions == nil {
		ideOptions = ide.Options{}
	}

	allowedOptions := []string{}
	for optionName := range ideOptions {
		allowedOptions = append(allowedOptions, optionName)
	}

	retMap := map[string]config.OptionValue{}
	for _, option := range options {
		splitted := strings.Split(option, "=")
		if len(splitted) == 1 {
			return nil, fmt.Errorf("invalid option '%s', expected format KEY=VALUE", option)
		}

		key := strings.ToUpper(strings.TrimSpace(splitted[0]))
		value := strings.Join(splitted[1:], "=")
		ideOption, ok := ideOptions[key]
		if !ok {
			return nil, fmt.Errorf("invalid option '%s', allowed options are: %v", key, allowedOptions)
		}

		if ideOption.ValidationPattern != "" {
			matcher, err := regexp.Compile(ideOption.ValidationPattern)
			if err != nil {
				return nil, err
			}

			if !matcher.MatchString(value) {
				if ideOption.ValidationMessage != "" {
					return nil, fmt.Errorf("%s", ideOption.ValidationMessage)
				}

				return nil, fmt.Errorf("invalid value '%s' for option '%s', has to match the following regEx: %s", value, key, ideOption.ValidationPattern)
			}
		}

		if len(ideOption.Enum) > 0 {
			found := false
			for _, e := range ideOption.Enum {
				if value == e {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("invalid value '%s' for option '%s', has to match one of the following values: %v", value, key, ideOption.Enum)
			}
		}

		retMap[key] = config.OptionValue{
			Value:        value,
			UserProvided: true,
		}
	}

	return retMap, nil
}

func detect() string {
	if command.Exists("code") {
		return string(config.IDEVSCode)
	}

	return string(config.IDEOpenVSCode)
}
