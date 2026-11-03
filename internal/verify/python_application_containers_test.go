package verify

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gitdek/invariant/internal/toolchain"
)

// pythonApplicationContainers lists only containers owned by this test process.
// Docker matches names with a leading slash; anchoring the name and including
// the PID's trailing separator excludes embedded names and PID prefix matches.
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
