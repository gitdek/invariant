package verify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

func TestCommandReplayRecordsAreCompleteAndUnambiguous913(t *testing.T) {
	state := tlc.ReplayState{"n": {Kind: "integer", Atom: "9007199254740993"}}
	result := BugReplayResult{Version: 1, RequestID: "request", InvocationID: "fresh", Outcome: "refused", Initial: state,
		Calls: []BugReplayCall{{Index: 0, Action: "Deliver", Args: []tlc.ReplayValue{{Kind: "integer", Atom: "9007199254740993"}}, Before: state, After: state, Dispatched: true, Outcome: "refused"}}}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"healthy", "duplicate nested key", "missing index", "null index", "trailing", "unknown", "symlink", "hardlink", "directory", "invalid UTF-8"} {
		t.Run(fault, func(t *testing.T) {
			dir := t.TempDir()
			body := string(raw)
			switch fault {
			case "duplicate nested key":
				body = strings.Replace(body, `"dispatched":true`, `"dispatched":false,"dispatched":true`, 1)
			case "missing index":
				body = strings.Replace(body, `"index":0,`, "", 1)
			case "null index":
				body = strings.Replace(body, `"index":0`, `"index":null`, 1)
			case "trailing":
				body += "{}"
			case "unknown":
				body = strings.Replace(body, `"version":1`, `"version":1,"extra":true`, 1)
			case "invalid UTF-8":
				body = strings.Replace(body, "Deliver", string([]byte{0xff}), 1)
			}
			path := filepath.Join(dir, "result.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if fault == "hardlink" {
				if err := os.Link(path, filepath.Join(dir, "other.json")); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "symlink" || fault == "directory" {
				if err := os.Rename(path, filepath.Join(dir, "other.json")); err != nil {
					t.Fatal(err)
				}
				if fault == "symlink" {
					err = os.Symlink("other.json", path)
				} else {
					err = os.Mkdir(path, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			var got BugReplayResult
			err := readReplayRecord(dir, "result.json", &got)
			if (err == nil) != (fault == "healthy") {
				t.Fatalf("record %s: %v", fault, err)
			}
			if fault == "healthy" && !sameReplayCalls(got.Calls, result.Calls) {
				t.Fatal("exact integer atoms changed")
			}
		})
	}
}

func TestCommandReplayKeepsPristineAdmittedSource913(t *testing.T) {
	src := t.TempDir()
	for _, name := range []string{"driver.ts", "out/selected.ts", "bin/selected.ts"} {
		if err := writeStaged(filepath.Join(src, name), []byte("original")); err != nil {
			t.Fatal(err)
		}
	}
	p := &project.Project{Manifest: project.Manifest{Language: "typescript"}, Lock: project.Lock{Statements: []project.Statement{{Name: "Bad", Kind: project.Bug, CodeBehavior: "current"}}}}
	s, err := keepCommandReplay(p, src, "pinned-image", "driver.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := os.WriteFile(filepath.Join(src, "driver.ts"), []byte("sample mutation"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"driver.ts", "out/selected.ts", "bin/selected.ts"} {
		b, err := os.ReadFile(filepath.Join(s.dir, "src", name))
		if err != nil || string(b) != "original" {
			t.Fatalf("admitted source changed: %s %s %v", name, b, err)
		}
	}
	dir := s.dir
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("snapshot leaked: %v", err)
	}
}
