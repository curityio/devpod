package agentbin

import (
	"errors"
	"testing"

	"gotest.tools/assert"
)

func TestOpenNotEmbedded(t *testing.T) {
	_, err := Open("riscv64")
	assert.Assert(t, errors.Is(err, ErrNotEmbedded))
}
