package verify

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gitdek/invariant/internal/toolchain"
)

// pythonApplicationContainers lists only containers named by this process.
// Docker matches names with a leading slash; the PID's trailing hyphen keeps
// another process with a longer PID from matching the same prefix.
func pythonApplicationContainers(ctx context.Context) (map[string]bool, error) {
	filter := fmt.Sprintf("name=^/invariant-%d-", os.Getpid())
	b, err := toolchain.Docker(ctx, "ps", "-aq", "--no-trunc", "--filter", filter).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list containers: %w: %s", err, strings.TrimSpace(string(b)))
	}
	containers := map[string]bool{}
	for _, id := range strings.Fields(string(b)) {
		containers[id] = true
	}
	return containers, nil
}
