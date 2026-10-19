//go:build devpod_agent

package agentbin

import "embed"

// IsAgent is true in the agent binaries, which embed no other agents.
const IsAgent = true

// files is empty: the agent binaries embed no other agents.
var files embed.FS
