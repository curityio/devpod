package container

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/loft-sh/devpod/pkg/agent"
	agentd "github.com/loft-sh/devpod/pkg/daemon/agent"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type DaemonCmd struct {
	Timeout string
	Log     log.Logger
}

// NewDaemonCmd creates the merged daemon command.
func NewDaemonCmd() *cobra.Command {
	cmd := &DaemonCmd{
		Log: log.NewStreamLogger(os.Stdout, os.Stderr, logrus.InfoLevel),
	}
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Starts the DevPod container daemon and monitors container activity if timeout is set",
		Args:  cobra.NoArgs,
		RunE:  cmd.Run,
	}
	daemonCmd.Flags().StringVar(&cmd.Timeout, "timeout", "", "The timeout to stop the container after")
	return daemonCmd
}

func (cmd *DaemonCmd) Run(c *cobra.Command, args []string) error {
	ctx := c.Context()
	errChan := make(chan error, 4)
	var wg sync.WaitGroup

	// Prepare timeout if specified.
	var timeoutDuration time.Duration
	if cmd.Timeout != "" {
		var err error
		timeoutDuration, err = time.ParseDuration(cmd.Timeout)
		if err != nil {
			return errors.Wrap(err, "failed to parse timeout duration")
		}
		if timeoutDuration > 0 {
			if err := setupActivityFile(); err != nil {
				return err
			}
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var tasksStarted bool

	// Start process reaper.
	if os.Getpid() == 1 {
		wg.Add(1)
		go runReaper(ctx, errChan, &wg)
	}

	// Start timeout monitor.
	if timeoutDuration > 0 {
		tasksStarted = true
		wg.Add(1)
		go runTimeoutMonitor(ctx, timeoutDuration, errChan, &wg)
	}

	// In case no task is configured, just wait indefinitely.
	if !tasksStarted {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ctx.Done()
		}()
	}

	// Listen for OS termination signals.
	go handleSignals(ctx, errChan)

	// Wait until an error (or termination signal) occurs.
	err := <-errChan
	cancel()
	wg.Wait()

	if err != nil {
		cmd.Log.Errorf("Daemon error: %v", err)
		os.Exit(1)
	}
	os.Exit(0)
	return nil // Unreachable but needed.
}

// setupActivityFile creates and sets permissions on the container activity file.
func setupActivityFile() error {
	if err := os.WriteFile(agent.ContainerActivityFile, nil, 0777); err != nil {
		return err
	}
	return os.Chmod(agent.ContainerActivityFile, 0777)
}

// runReaper starts the process reaper and waits for context cancellation.
func runReaper(ctx context.Context, errChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	agentd.RunProcessReaper()
	<-ctx.Done()
}

// runTimeoutMonitor monitors the activity file and signals an error if the timeout is exceeded.
func runTimeoutMonitor(ctx context.Context, duration time.Duration, errChan chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stat, err := os.Stat(agent.ContainerActivityFile)
			if err != nil {
				continue
			}
			if !stat.ModTime().Add(duration).After(time.Now()) {
				errChan <- errors.New("timeout reached, terminating daemon")
				return
			}
		}
	}
}

// handleSignals listens for OS termination signals and sends an error through errChan.
func handleSignals(ctx context.Context, errChan chan<- error) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-sigChan:
		errChan <- fmt.Errorf("received signal: %v", sig)
	case <-ctx.Done():
	}
}
