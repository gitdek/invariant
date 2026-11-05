package plumbing_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/decisions"
)

func TestPlanCheckDoesNotOpenMachineDecisionStore(t *testing.T) {
	config := t.TempDir()
	t.Setenv("HOME", config)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("AppData", config)
	path, err := decisions.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Opening this as SQLite would fail; replacing it would change the snapshot.
	if err := os.WriteFile(path, []byte("machine store must stay untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot529(t, config)
	sandbox := sandbox529(t)
	root, recorded := repo529(t, true)
	ws := t.TempDir()
	for _, id := range []string{fmt.Sprintf("D-%04d", 9999), recorded} {
		draft529(t, ws, map[string]string{
			"sample/pending_accept_test.go": source529(t, "TestPending", fmt.Sprintf("%q", id), "Fixture citation.", false),
		})
		c := check529(t, ws, root, sandbox)
		if id == recorded {
			if c.Problem != "" {
				t.Fatalf("recorded citation was refused: %s", c.Feedback())
			}
		} else if !strings.Contains(c.Problem, id+", which isn't a decision") {
			t.Fatalf("unknown citation lost its diagnostic: %s", c.Feedback())
		}
		if !reflect.DeepEqual(before, snapshot529(t, config)) {
			t.Fatal("plan check changed the machine's decision store")
		}
	}
}

func TestPlanCheckDistinguishesEmptyAndAbsentDecisionJournals(t *testing.T) {
	sandbox := sandbox529(t)
	root, _ := repo529(t, false)
	write529(t, root, "decisions/log.md", "# Decisions\n\n"+decisions.TableStart+"\n"+decisions.TableEnd+"\n")
	ws := t.TempDir()
	draft529(t, ws, map[string]string{
		"sample/pending_accept_test.go": source529(t, "TestPending", `fmt.Sprintf("D-%04d", 9999)`, "Runtime fixture.", false),
	})
	if c := check529(t, ws, root, sandbox); c.Problem != "" {
		t.Fatalf("a log without a journal changed behavior: %s", c.Feedback())
	}
	if err := os.MkdirAll(filepath.Join(root, "decisions", "journal"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := check529(t, ws, root, sandbox)
	if !strings.Contains(c.Problem, "decisions/log.md isn't in the journal yet") {
		t.Fatalf("an empty journal bypassed graph validation: %s", c.Feedback())
	}
}

func TestPlanCheckWithJournalStillRequiresCompletedAcceptanceFailures(t *testing.T) {
	for _, status := range []string{"skipped", "running", "not_run"} {
		t.Run(status, func(t *testing.T) {
			sandbox := sandbox529(t)
			bin := filepath.SplitList(os.Getenv("PATH"))[0]
			write529(t, bin, "docker", strings.Replace(decisionDocker529, "status=failed", "status="+status, 1))
			root, recorded := repo529(t, true)
			ws := t.TempDir()
			draft529(t, ws, map[string]string{
				"sample/pending_accept_test.go": source529(t, "TestPending", fmt.Sprintf("%q", recorded), "Recorded citation.", false),
			})
			c := check529(t, ws, root, sandbox)
			if !strings.Contains(c.Problem, "did not complete with failure evidence") || !strings.Contains(c.Problem, "TestPending") || !strings.Contains(c.Problem, status) {
				t.Fatalf("a valid graph bypassed unfinished acceptance evidence: %s", c.Feedback())
			}
		})
	}
}
