//go:build !devpod_agent

package agentbin

import "embed"

// IsAgent is true in the agent binaries, which embed no other agents.
const IsAgent = false

// bin/.gitignore is always there, so this builds even when the agent binaries are not.
//
//go:embed all:bin
var files embed.FS
