package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	client2 "github.com/loft-sh/devpod/pkg/client"
	"github.com/loft-sh/devpod/pkg/log"
	"github.com/loft-sh/devpod/pkg/provider"
	workspace2 "github.com/loft-sh/devpod/pkg/workspace"
	"github.com/sirupsen/logrus"
	"gotest.tools/assert"
)

// fakeClient is a workspace client whose Status calls status. Its other methods aren't implemented.
type fakeClient struct {
	client2.WorkspaceClient

	status func(ctx context.Context, options client2.StatusOptions) (client2.Status, error)
}

func (c *fakeClient) Status(ctx context.Context, options client2.StatusOptions) (client2.Status, error) {
	return c.status(ctx, options)
}

func listed(id string, status func(ctx context.Context, options client2.StatusOptions) (client2.Status, error)) workspace2.ListedClient {
	return workspace2.ListedClient{
		ID:        id,
		Workspace: &provider.Workspace{ID: id, Provider: provider.WorkspaceProviderConfig{Name: "test"}},
		Client:    &fakeClient{status: status},
	}
}

func returns(status client2.Status, err error) func(context.Context, client2.StatusOptions) (client2.Status, error) {
	return func(context.Context, client2.StatusOptions) (client2.Status, error) {
		return status, err
	}
}

func TestAllStatuses(t *testing.T) {
	cmd := &StatusCmd{StatusOptions: client2.StatusOptions{ContainerStatus: true}}
	workspaces := []workspace2.ListedClient{
		listed("running", func(_ context.Context, options client2.StatusOptions) (client2.Status, error) {
			assert.Assert(t, options.ContainerStatus)
			assert.Assert(t, options.MachineStatuses != nil)
			return client2.StatusRunning, nil
		}),
		listed("failing", returns("", errors.New("boom"))),
		listed("slow", func(ctx context.Context, _ client2.StatusOptions) (client2.Status, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}),
		{ID: "corrupt", Err: errors.New("load workspace config: bad json")},
		listed("stopped", returns(client2.StatusStopped, nil)),
	}

	statuses := cmd.allStatuses(context.Background(), "default", workspaces, 10*time.Millisecond)

	assert.DeepEqual(t, statuses, []client2.WorkspaceStatus{
		{ID: "running", Context: "default", Provider: "test", State: client2.StatusRunning},
		{ID: "failing", Context: "default", Provider: "test", Error: "boom"},
		{ID: "slow", Context: "default", Provider: "test", Error: "timed out after 10ms, you can wait longer via --timeout"},
		{ID: "corrupt", Context: "default", Error: "load workspace config: bad json"},
		{ID: "stopped", Context: "default", Provider: "test", State: client2.StatusStopped},
	})
}

func TestAllStatusesGivesEachWorkspaceItsOwnTimeout(t *testing.T) {
	cmd := &StatusCmd{}
	running := atomic.Int32{}
	maxRunning := atomic.Int32{}
	workspaces := []workspace2.ListedClient{}
	for range 3 * maxConcurrentStatus {
		workspaces = append(workspaces, listed("ws", func(ctx context.Context, _ client2.StatusOptions) (client2.Status, error) {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				current := maxRunning.Load()
				if n <= current || maxRunning.CompareAndSwap(current, n) {
					break
				}
			}

			// each status takes most of the timeout, so the later ones would time out if it were shared
			select {
			case <-time.After(150 * time.Millisecond):
				return client2.StatusRunning, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}))
	}

	statuses := cmd.allStatuses(context.Background(), "default", workspaces, time.Second)

	for _, status := range statuses {
		assert.Equal(t, status.Error, "")
		assert.Equal(t, status.State, client2.StatusRunning)
	}
	assert.Assert(t, maxRunning.Load() <= maxConcurrentStatus, "%d statuses retrieved at once", maxRunning.Load())
}

func TestAllStatusesStopsWhenCancelled(t *testing.T) {
	cmd := &StatusCmd{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspaces := []workspace2.ListedClient{}
	for range maxConcurrentStatus + 1 {
		workspaces = append(workspaces, listed("ws", func(ctx context.Context, _ client2.StatusOptions) (client2.Status, error) {
			cancel()
			<-ctx.Done()
			return "", ctx.Err()
		}))
	}

	statuses := cmd.allStatuses(ctx, "default", workspaces, 0)

	for _, status := range statuses {
		assert.Equal(t, status.Error, context.Canceled.Error())
	}
}

func TestPrintStatuses(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	out := log.NewStreamLogger(stdout, stderr, logrus.InfoLevel)
	logger := log.NewStreamLogger(stderr, stderr, logrus.InfoLevel)

	err := printStatuses([]client2.WorkspaceStatus{
		{ID: "running", Provider: "docker", State: client2.StatusRunning},
		{ID: "broken", Provider: "missing", Error: "couldn't find provider"},
	}, out, logger)

	assert.Error(t, err, "couldn't get the status of 1 of 2 workspaces")
	assert.Assert(t, containsRow(stdout.String(), "running", "docker", "Running"), stdout.String())
	assert.Assert(t, containsRow(stdout.String(), "broken", "missing", "Error"), stdout.String())
	assert.Assert(t, strings.Contains(stderr.String(), "Couldn't get the status of workspace 'broken': couldn't find provider"), stderr.String())
}

func TestPrintStatusesWithoutWorkspaces(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	out := log.NewStreamLogger(stdout, stderr, logrus.InfoLevel)
	logger := log.NewStreamLogger(stderr, stderr, logrus.InfoLevel)

	err := printStatuses(nil, out, logger)

	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout.String(), "There are no workspaces"), stdout.String())
	assert.Equal(t, stderr.String(), "")
}

// containsRow reports whether a line of the table has the cells, in order
func containsRow(table string, cells ...string) bool {
	for _, line := range strings.Split(table, "\n") {
		fields := []string{}
		for _, field := range strings.Split(line, "|") {
			fields = append(fields, strings.TrimSpace(field))
		}
		if strings.Join(fields, "|") == strings.Join(cells, "|") {
			return true
		}
	}

	return false
}
