package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The code a sandbox runs owns the directory the sandbox shares with the
// gate, and can leave a link there to any file on the host. The gate reads
// the driver's log, its count and its recording back from that directory,
// and puts the log's last lines in the result's message, which reaches the
// receipt. None of them may follow a link out of the directory (#816).
func TestSandboxOutputLinksAreNotFollowed816(t *testing.T) {
	const secret = "TOP-SECRET-816 a line of a file outside every sandbox"
	host := t.TempDir()
	hostFile := filepath.Join(host, "secret.txt")
	if err := os.WriteFile(hostFile, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	relative := func(out string) string {
		t.Helper()
		rel, err := filepath.Rel(out, hostFile)
		if err != nil {
			t.Fatal(err)
		}
		return rel
	}
	for _, how := range []string{"absolute", "relative"} {
		target := func(out string) string {
			if how == "relative" {
				return relative(out)
			}
			return hostFile
		}
		t.Run(how+" link in place of the driver's log", func(t *testing.T) {
			out := t.TempDir()
			if err := os.Symlink(target(out), filepath.Join(out, "driver.log")); err != nil {
				t.Fatal(err)
			}
			got := countOutcome(out, 1)
			if got.OK || strings.Contains(got.Message, "TOP-SECRET") || !strings.Contains(got.Message, "its log wasn't read") {
				t.Errorf("the failed count's message: %+v", got)
			}
		})
		t.Run(how+" link in place of the count", func(t *testing.T) {
			out := t.TempDir()
			// A count the gate would accept, if it read the host's file.
			if err := os.WriteFile(hostFile, []byte(`{"states": 7, "depth": 2}`), 0o600); err != nil {
				t.Fatal(err)
			}
			defer os.WriteFile(hostFile, []byte(secret+"\n"), 0o600)
			if err := os.Symlink(target(out), filepath.Join(out, "count.json")); err != nil {
				t.Fatal(err)
			}
			if got := countOutcome(out, 0); got.OK || got.States != 0 || !strings.Contains(got.Message, "wrote no count") {
				t.Errorf("a count read through a link out of the sandbox's output: %+v", got)
			}
		})
		t.Run(how+" link in place of the recording", func(t *testing.T) {
			out := t.TempDir()
			path := filepath.Join(out, "traces.json")
			if err := os.Symlink(target(out), path); err != nil {
				t.Fatal(err)
			}
			r, err := takeRecording(context.Background(), path)
			if err == nil {
				r.close()
				t.Fatal("a recording was taken through a link out of the sandbox's output")
			}
			if strings.Contains(err.Error(), "TOP-SECRET") {
				t.Errorf("the refusal quotes the host's file: %v", err)
			}
			if b, _ := os.ReadFile(hostFile); string(b) != secret+"\n" {
				t.Error("the host's file was moved or changed")
			}
		})
	}

	// What the sandbox itself wrote reads as before: a regular file, and a
	// link to a file beside it.
	out := t.TempDir()
	for name, text := range map[string]string{"driver.log": "boom\nlast line\n", "real.json": `{"states": 7, "depth": 2}`} {
		if err := os.WriteFile(filepath.Join(out, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := countOutcome(out, 1); got.Message != "the driver failed one size larger:\nboom\nlast line" {
		t.Errorf("a failed count's message = %q", got.Message)
	}
	if err := os.Symlink("real.json", filepath.Join(out, "count.json")); err != nil {
		t.Fatal(err)
	}
	if got := countOutcome(out, 0); !got.OK || got.States != 7 || got.Depth != 2 {
		t.Errorf("a count behind a link inside the output directory: %+v", got)
	}
	if got := countOutcome(t.TempDir(), 1); got.Message != "the driver failed one size larger:\n" {
		t.Errorf("a failed count with no log: %+v", got)
	}
	if got := countOutcome(t.TempDir(), 124); !got.Unfinished {
		t.Errorf("a count that timed out: %+v", got)
	}

	// A long log keeps its end, which says how the run ended.
	long := t.TempDir()
	if err := os.WriteFile(filepath.Join(long, "driver.log"), []byte(strings.Repeat("x", driverLogLimit+500)+"\nthe last line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := countOutcome(long, 1); !strings.HasSuffix(got.Message, "the last line") || len(got.Message) > driverLogLimit+100 {
		t.Errorf("a long log's message ends %q and is %d bytes", got.Message[max(0, len(got.Message)-30):], len(got.Message))
	}
}

// A pipe where a file should be is refused at once: opening it never waits
// for a writer that isn't coming.
func TestSandboxOutputPipesDoNotBlock816(t *testing.T) {
	out := t.TempDir()
	for _, name := range []string{"driver.log", "count.json", "traces.json"} {
		if err := syscall.Mkfifo(filepath.Join(out, name), 0o600); err != nil {
			t.Skipf("no named pipes here: %v", err)
		}
	}
	done := make(chan string, 1)
	go func() {
		var problems []string
		if got := countOutcome(out, 1); !strings.Contains(got.Message, "isn't a regular file") {
			problems = append(problems, "the log: "+got.Message)
		}
		if got := countOutcome(out, 0); got.OK || !strings.Contains(got.Message, "wrote no count") {
			problems = append(problems, "the count: "+got.Message)
		}
		if r, err := takeRecording(context.Background(), filepath.Join(out, "traces.json")); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			r.close()
			problems = append(problems, "the recording was taken")
		}
		done <- strings.Join(problems, "; ")
	}()
	select {
	case problems := <-done:
		if problems != "" {
			t.Error(problems)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reading a pipe from a sandbox's output blocked")
	}
}
