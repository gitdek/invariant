package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamingApplicationModuleHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "module.tla")
	text := strings.Repeat("recorded action\n", 10000)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := pythonApplicationModelHash(context.Background(), path)
	if err != nil || got != pythonApplicationHash([]byte(text)) {
		t.Fatalf("streaming changed the module fingerprint: %s %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := pythonApplicationModelHash(ctx, path); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled module hash supplied evidence: %s %v", got, err)
	}
}
