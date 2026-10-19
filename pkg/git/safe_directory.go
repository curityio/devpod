package git

import (
	"context"
	"fmt"
	"strings"
)

// AddSafeDirectory adds dir to safe.directory in the system git config, so that git uses the repository in dir
// whoever owns it.
func AddSafeDirectory(ctx context.Context, dir string) error {
	out, _ := CommandContext(ctx, nil, "config", "--system", "--get-all", "safe.directory").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if value := strings.TrimSpace(line); value == dir || value == "*" {
			return nil
		}
	}
	out, err := CommandContext(ctx, nil, "config", "--system", "--add", "safe.directory", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("add %s to safe.directory: %s: %w", dir, strings.TrimSpace(string(out)), err)
	}
	return nil
}
