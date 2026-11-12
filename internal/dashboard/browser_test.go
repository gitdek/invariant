package dashboard

import (
	"context"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// browserDOM loads url in a headless browser, lets the page's scripts run
// through budget milliseconds of virtual time, and returns the DOM the page
// ends with. A loaded CI runner can take most of a minute to start a
// browser, and the hard minute each check once gave it killed it there,
// which failed main's gate and pull requests that touched nothing here. So
// a try has three minutes, and a try that runs out gets one more.
func browserDOM(t *testing.T, browser, url string, budget int) ([]byte, error) {
	t.Helper()
	var out []byte
	var err error
	for try := 1; try <= 2; try++ {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		cmd := exec.CommandContext(ctx, browser, "--headless", "--no-sandbox", "--disable-gpu", "--disable-background-networking",
			"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1", "--user-data-dir="+t.TempDir(),
			fmt.Sprintf("--virtual-time-budget=%d", budget), "--dump-dom", url)
		out, err = cmd.CombinedOutput()
		ranOut := ctx.Err() != nil
		cancel()
		if !ranOut {
			break
		}
		t.Logf("the browser didn't finish within three minutes, on try %d", try)
	}
	return out, err
}
