package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/spf13/cobra"
)

var (
	ErrContainerAlreadyRunning = errors.New("container is already running")
	ErrNoEntrypoint            = errors.New("no entrypoint specified")
)

var ContainerConfigOutput = "/.dockerless/container.json"

var ContainerPID = "/.dockerless/pid"

type StartCmd struct {
	Entrypoint []string
	Cmd        []string

	User   string
	Env    []string
	Labels []string

	Wait bool
}

// NewStartCmd returns a new start command
func NewStartCmd() *cobra.Command {
	cmd := &StartCmd{}
	cobraCmd := &cobra.Command{
		Use:           "start",
		Short:         "Starts the container",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run()
		},
	}

	cobraCmd.Flags().BoolVar(&cmd.Wait, "wait", false, "If true, will wait until the container is built.")
	cobraCmd.Flags().StringVar(&cmd.User, "user", "", "The container user to run the entrypoint with.")
	cobraCmd.Flags().StringArrayVar(&cmd.Entrypoint, "entrypoint", []string{}, "The entrypoint to use.")
	cobraCmd.Flags().StringArrayVar(&cmd.Cmd, "cmd", []string{}, "The cmds to use.")
	cobraCmd.Flags().StringArrayVar(&cmd.Env, "env", []string{}, "Extra environment variables to start the container with.")
	cobraCmd.Flags().StringArrayVar(&cmd.Labels, "labels", []string{}, "Labels to add to the container.")
	return cobraCmd
}

func (cmd *StartCmd) Run() error {
	// check if we already have built the image
	if isContainerRunning() {
		return ErrContainerAlreadyRunning
	}

	// get the built image config
	var (
		configFile *v1.ConfigFile
		err        error
	)
	for {
		configFile, err = readImageConfig()
		if err != nil {
			if !cmd.Wait {
				return err
			}

			time.Sleep(time.Second)
			continue
		}

		break
	}

	// entrypoint
	if len(cmd.Entrypoint) > 0 {
		configFile.Config.Entrypoint = cmd.Entrypoint
	}

	// cmd
	if len(cmd.Cmd) > 0 {
		configFile.Config.Cmd = cmd.Cmd
	}

	// add labels
	if len(cmd.Labels) > 0 {
		if configFile.Config.Labels == nil {
			configFile.Config.Labels = map[string]string{}
		}

		for _, label := range cmd.Labels {
			splitted := strings.SplitN(label, "=", 2)
			if len(splitted) == 2 {
				configFile.Config.Labels[splitted[0]] = splitted[1]
			}
		}
	}

	// add environment variables
	configFile.Config.Env = append(configFile.Config.Env, cmd.Env...)

	// user
	if cmd.User != "" {
		configFile.Config.User = cmd.User
	}

	// set to root if not specified
	if configFile.Config.WorkingDir == "" {
		configFile.Config.WorkingDir = "/"
	} else {
		// create working dir
		err = os.MkdirAll(configFile.Config.WorkingDir, 0777)
		if err != nil {
			return fmt.Errorf("create workspace folder: %w", err)
		}
	}

	// get user info
	homeDir, credential, err := getUserInfo(configFile.Config.User)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	// prepare the command to run
	entrypoint := []string{}
	entrypoint = append(entrypoint, configFile.Config.Entrypoint...)
	entrypoint = append(entrypoint, configFile.Config.Cmd...)
	if len(entrypoint) == 0 {
		return ErrNoEntrypoint
	}

	// build the container env
	containerEnv := getContainerEnv(homeDir, configFile.Config.Env, os.Environ())

	// write container config
	out, err := json.MarshalIndent(configFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config file: %w", err)
	}

	err = os.WriteFile(ContainerConfigOutput, out, 0666)
	if err != nil {
		return fmt.Errorf("write container config: %w", err)
	}

	err = os.WriteFile(ContainerPID, []byte(strconv.Itoa(os.Getpid())), 0666)
	if err != nil {
		return fmt.Errorf("write container pid: %w", err)
	}

	// set container env before execution so we find executables
	for _, env := range containerEnv {
		splitted := strings.SplitN(env, "=", 2)
		if len(splitted) != 2 {
			continue
		}

		_ = os.Setenv(splitted[0], splitted[1])
	}

	// start command
	containerCmd := exec.Command(entrypoint[0], entrypoint[1:]...)
	containerCmd.SysProcAttr = &syscall.SysProcAttr{}
	containerCmd.SysProcAttr.Credential = credential
	containerCmd.Stdout = os.Stdout
	containerCmd.Stderr = os.Stderr
	containerCmd.Stdin = os.Stdin
	containerCmd.Dir = configFile.Config.WorkingDir
	containerCmd.Env = containerEnv

	err = containerCmd.Run()
	if err != nil {
		return fmt.Errorf("run container: %w", err)
	}

	return nil
}

func readImageConfig() (*v1.ConfigFile, error) {
	out, err := os.ReadFile(ImageConfigOutput)
	if err != nil {
		return nil, fmt.Errorf("read image config: %w", err)
	}

	configFile := &v1.ConfigFile{}
	err = json.Unmarshal(out, configFile)
	if err != nil {
		return nil, fmt.Errorf("unmarshal image config: %w", err)
	}

	return configFile, nil
}

func isContainerRunning() bool {
	pid, err := os.ReadFile(ContainerPID)
	if err == nil {
		isRunning, _ := isProcessRunning(string(pid))
		if isRunning {
			return true
		}
	}

	return false
}

func isProcessRunning(pid string) (bool, error) {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return false, fmt.Errorf("parse pid: %w", err)
	}

	process, err := os.FindProcess(parsedPid)
	if err != nil {
		return false, fmt.Errorf("find process: %w", err)
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return false, nil
	}

	return true, nil
}

func setPath(containerEnvs []string) {
	for _, containerEnv := range containerEnvs {
		splitted := strings.SplitN(containerEnv, "=", 2)
		if len(splitted) == 2 && strings.ToUpper(splitted[0]) == "PATH" {
			_ = os.Setenv(splitted[0], splitted[1])
		}
	}
}

// dockerlessEnv is the environment of the dockerless image (see its Dockerfile), which the
// container gets from the image it was built from instead.
var dockerlessEnv = map[string]string{
	"HOME":         "/root",
	"USER":         "root",
	"PATH":         "/usr/local/bin:/.dockerless/bin",
	"KANIKO_DIR":   "/.dockerless",
	"SSL_CERT_DIR": "/.dockerless/ssl/certs",
}

// getContainerEnv returns the container's environment, as docker would build it: HOME, the built
// image's environment, then the one the container was started with (processEnv, minus what the
// dockerless image and DevPod set for dockerless itself), each overriding the one before.
func getContainerEnv(homeDir string, imageEnv []string, processEnv []string) []string {
	env := []string{"HOME=" + homeDir}
	env = append(env, imageEnv...)
	for _, e := range processEnv {
		name, value, _ := strings.Cut(e, "=")
		if name == "DOCKERLESS" || strings.HasPrefix(name, "DOCKERLESS_") || dockerlessEnv[name] == value {
			continue
		}
		env = append(env, e)
	}
	setPath(env)
	return env
}

// getUserInfo returns the home directory and credentials of a USER value, which docker accepts
// as user, uid, user:group, uid:gid, user:gid or uid:group. A uid or gid needs no entry in
// /etc/passwd or /etc/group; a uid without one runs with gid 0 and HOME=/.
func getUserInfo(spec string) (string, *syscall.Credential, error) {
	name, group, hasGroup := strings.Cut(spec, ":")
	if name == "" {
		name = "root"
	}

	homeDir := "/"
	credential := &syscall.Credential{Groups: []uint32{}}
	userObj, err := getUser(name)
	if err == nil {
		uid, err := strconv.ParseUint(userObj.Uid, 10, 32)
		if err != nil {
			return "", nil, fmt.Errorf("parse uid: %w", err)
		}
		gid, err := strconv.ParseUint(userObj.Gid, 10, 32)
		if err != nil {
			return "", nil, fmt.Errorf("parse gid: %w", err)
		}
		credential.Uid = uint32(uid)
		credential.Gid = uint32(gid)

		groupIDs, err := userObj.GroupIds()
		if err == nil {
			for _, groupID := range groupIDs {
				parsed, err := strconv.ParseUint(groupID, 10, 32)
				if err == nil {
					credential.Groups = append(credential.Groups, uint32(parsed))
				}
			}
		}

		homeDir = userObj.HomeDir
		if homeDir == "" {
			homeDir = "/root"
		}
	} else {
		uid, parseErr := strconv.ParseUint(name, 10, 32)
		if parseErr != nil {
			return "", nil, err
		}
		credential.Uid = uint32(uid)
	}

	if hasGroup {
		gid, err := getGroupID(group)
		if err != nil {
			return "", nil, err
		}
		credential.Gid = gid
	}

	return homeDir, credential, nil
}

func getUser(name string) (*user.User, error) {
	_, err := strconv.Atoi(name)
	if err == nil {
		user, err := user.LookupId(name)
		if err != nil {
			return nil, fmt.Errorf("failed to lookup user by id: %w", err)
		}

		return user, nil
	}

	user, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup user: %w", err)
	}

	return user, nil
}

func getGroupID(name string) (uint32, error) {
	gid, err := strconv.ParseUint(name, 10, 32)
	if err == nil {
		return uint32(gid), nil
	}

	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("failed to lookup group: %w", err)
	}

	gid, err = strconv.ParseUint(group.Gid, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse gid: %w", err)
	}

	return uint32(gid), nil
}
