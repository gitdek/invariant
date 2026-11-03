package verify

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Docker matches names with their leading slash. The trailing dash keeps a
// different PID that starts with ours out of this process's leak checks.
func pythonApplicationContainers(ctx context.Context) (map[string]bool, error) {
	filter := fmt.Sprintf("name=^/invariant-%d-", os.Getpid())
	b, err := toolchain.Docker(ctx, "ps", "-aq", "--filter", filter).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list containers: %w\n%s", err, b)
	}
	containers := map[string]bool{}
	for _, id := range strings.Fields(string(b)) {
		containers[id] = true
	}
	return containers, nil
}
