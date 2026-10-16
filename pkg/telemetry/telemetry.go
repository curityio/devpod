// Package telemetry holds the hooks the CLI calls to report usage.
// Telemetry is disabled: CollectorCLI never sends anything until a Curity backend exists to collect it.
package telemetry

import devpodclient "github.com/loft-sh/devpod/pkg/client"

const UIEnvVar = "DEVPOD_UI"

var CollectorCLI CLICollector = &noopCollector{}

type CLICollector interface {
	RecordCLI(err error)
	SetClient(client devpodclient.BaseWorkspaceClient)

	// Flush makes sure all events are sent to the backend
	Flush()
}

type noopCollector struct{}

func (n *noopCollector) RecordCLI(err error)                               {}
func (n *noopCollector) SetClient(client devpodclient.BaseWorkspaceClient) {}

func (n *noopCollector) Flush() {}
