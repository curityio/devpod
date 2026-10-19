// Package agentbin holds the Linux agent binaries that DevPod copies into machines and dev containers.
//
// hack/build-agents.sh builds them into bin/ (gzipped) before the CLI is built, with the same version as the CLI,
// so that the agent always matches the CLI that injects it. Builds that skip it, such as a plain
// `go build ./...`, have no agent binaries. The agent binaries themselves are built with the devpod_agent build
// tag and don't embed anything: an agent only ever injects itself.
package agentbin

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
)

// ErrNotEmbedded is returned by Open when this binary has no agent binary for the requested architecture.
var ErrNotEmbedded = errors.New("agent binary not embedded")

// FileName is the name of the agent binary for linux/<arch>, as built by hack/build-agents.sh (without the .gz
// suffix).
func FileName(arch string) string {
	return "devpod-linux-" + arch
}

// Open returns the decompressed agent binary for linux/<arch>.
func Open(arch string) (io.ReadCloser, error) {
	file, err := files.Open("bin/" + FileName(arch) + ".gz")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w for linux/%s", ErrNotEmbedded, arch)
		}
		return nil, err
	}

	reader, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("decompress agent binary for linux/%s: %w", arch, err)
	}

	return &gzipFile{Reader: reader, file: file}, nil
}

type gzipFile struct {
	*gzip.Reader
	file fs.File
}

func (g *gzipFile) Close() error {
	return errors.Join(g.Reader.Close(), g.file.Close())
}
