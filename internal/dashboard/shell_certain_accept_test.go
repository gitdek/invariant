package dashboard

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type shell211Form struct {
	name, command, kind string
	files               []string
}

// Each regression pairs ordinary commands with opaque forms of the same
// operation. The ordinary cases require useful classification; a reader
// that labels every shell call other must fail each acceptance test.
// All tempting operands exist, including patterns and option arguments,
// so checking file existence alone cannot satisfy the privacy contract.
func shell211CheckForms(t *testing.T, forms []shell211Form) {
	t.Helper()
	for _, provider := range []string{"codex", "claude-code"} {
		t.Logf("transcript provider: %s", provider)
		for _, c := range forms {
			t.Logf("command form: %s", c.name)
			f := newShell211Fixture(t, provider)
			for _, path := range []string{"app.go", "internal/app.go", "safe.go", "PATTERN", "SECRET", "NAME=VALUE"} {
				f.file(t, path)
			}
			wantFiles := map[string][2]int{}
			for _, path := range c.files {
				wantFiles[path] = [2]int{1, 0}
			}
			// Rejection must hold while the command is running too;
			// publishing a path and removing it at completion leaks it.
			f.append(t, f.start(t, "form", c.command, 1))
			for _, completed := range []bool{false, true} {
				if completed {
					f.append(t, f.done(t, "form", c.command, 3))
				}
				l := f.read(t)
				if want := map[string]int{c.kind: 1}; !reflect.DeepEqual(l.Tools, want) {
					t.Errorf("completed=%v: tools = %v; want %v", completed, l.Tools, want)
				}
				if got := shell211Files(l); !reflect.DeepEqual(got, wantFiles) || len(l.Files) != len(wantFiles) {
					t.Errorf("completed=%v: files = %+v; want %v", completed, l.Files, wantFiles)
				}
				if len(l.Marks) != 1 {
					t.Fatalf("completed=%v: marks = %+v; want one invocation", completed, l.Marks)
				}
				end := -1
				if completed {
					end = 3
				} else if l.Now != c.kind {
					t.Errorf("running kind = %q; want %q", l.Now, c.kind)
				}
				shell211Mark(t, f, l.Marks[0], c.kind, 1, end)

				rec := httptest.NewRecorder()
				f.server.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/live.json", nil))
				if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("public live response: %d, cache %q", rec.Code, rec.Header().Get("Cache-Control"))
				}
				private := []string{"PATTERN", "SECRET", "PRIVATE_", f.workspace, c.command,
					`"command"`, `"commands"`, `"input"`, `"output_text"`}
				if c.kind == "other" {
					private = append(private, "app.go", "safe.go", "NAME=VALUE")
				}
				for _, text := range private {
					if strings.Contains(rec.Body.String(), text) {
						t.Errorf("completed=%v: public JSON contains command material %q", completed, text)
					}
				}
			}
		}
	}
}

// POSIXLY_CORRECT can change grep's interpretation of operands. Every
// assignment prefix is opaque, including env and assignments nested in a
// shell wrapper or a compound invocation. No file from any part of that
// invocation may be published, even a certain read encountered first.
func TestShellAssignmentsMakeWholeInvocationOther(t *testing.T) {
	shell211CheckForms(t, []shell211Form{
		{"ordinary-search", `grep -n -e PATTERN -- app.go`, "search", []string{"app.go"}},
		{"equals-in-file-operand", `cat 'NAME=VALUE'`, "read", []string{"NAME=VALUE"}},
		{"posix-grep", `POSIXLY_CORRECT=1 grep PATTERN -e SECRET app.go`, "other", nil},
		{"quoted-assignment", `POSIXLY_CORRECT='1' grep PATTERN -e SECRET app.go`, "other", nil},
		{"env-assignment", `env POSIXLY_CORRECT=1 grep PATTERN -e SECRET app.go`, "other", nil},
		{"locale-assignment", `LC_ALL=C cat app.go`, "other", nil},
		{"assignment-in-wrapper", `sh -c 'POSIXLY_CORRECT=1 grep PATTERN -e SECRET app.go'`, "other", nil},
		{"certain-read-first", `cat safe.go && POSIXLY_CORRECT=1 grep PATTERN -e SECRET app.go`, "other", nil},
		{"certain-read-last", `LC_ALL=C cat app.go && cat safe.go`, "other", nil},
	})
}

// A path's basename does not establish which command it is. In particular
// an external cd cannot change the parent shell's cwd. Both candidate
// app.go files exist, so misreporting internal/app.go is observable.
func TestShellPathNamedCommandsMakeWholeInvocationOther(t *testing.T) {
	shell211CheckForms(t, []shell211Form{
		{"ordinary-cd", `cd internal && cat app.go`, "read", []string{"internal/app.go"}},
		{"ordinary-wrapper", `sh -c 'cd internal && cat app.go'`, "read", []string{"internal/app.go"}},
		{"external-cd", `/usr/bin/cd internal && cat app.go`, "other", nil},
		{"relative-external-cd", `./cd internal && cat app.go`, "other", nil},
		{"absolute-reader", `/usr/bin/cat app.go`, "other", nil},
		{"relative-reader", `./cat app.go`, "other", nil},
		{"absolute-searcher", `/usr/bin/grep -n -e PATTERN -- app.go`, "other", nil},
		// The whole command may be a system shell by its path, as Codex runs
		// every command, but nothing inside it may name a path (D-0160).
		{"system-shell", `/bin/sh -c 'cat app.go'`, "read", []string{"app.go"}},
		{"codex-wrapper", `/bin/zsh -lc 'cd internal && cat app.go'`, "read", []string{"internal/app.go"}},
		{"other-shell-path", `/opt/homebrew/bin/bash -c 'cat app.go'`, "other", nil},
		{"absolute-shell-in-wrapper", `sh -c '/bin/sh -c "cat app.go"'`, "other", nil},
		{"external-cd-in-codex-wrapper", `/bin/zsh -lc '/usr/bin/cd internal && cat app.go'`, "other", nil},
		{"external-cd-in-wrapper", `sh -c '/usr/bin/cd internal && cat app.go'`, "other", nil},
		{"certain-read-first", `cat safe.go && /usr/bin/cd internal && cat app.go`, "other", nil},
	})
}

// An option outside the modeled vocabulary cannot be guessed to take no
// argument. These deliberately unknown options must reject the whole call,
// whether before or after a file operand, in a short-option cluster or in
// a wrapper. Existing supported options still classify and publish files.
func TestShellUnmodeledOptionsMakeWholeInvocationOther(t *testing.T) {
	shell211CheckForms(t, []shell211Form{
		{"ordinary-read-options", `head -n 5 -- app.go`, "read", []string{"app.go"}},
		{"ordinary-search-options", `rg -n --glob '*.go' -e PATTERN -- app.go`, "search", []string{"app.go"}},
		{"unknown-grep-option", `grep --invariant-unknown SECRET PATTERN app.go`, "other", nil},
		{"unknown-rg-option", `rg --invariant-unknown SECRET PATTERN app.go`, "other", nil},
		{"option-with-value", `rg --invariant-unknown=SECRET PATTERN app.go`, "other", nil},
		{"option-after-file", `grep -n -e PATTERN app.go --invariant-unknown SECRET`, "other", nil},
		{"unknown-short-option", `grep -nQ SECRET PATTERN app.go`, "other", nil},
		{"unknown-read-option", `cat --invariant-unknown SECRET app.go`, "other", nil},
		{"unknown-list-option", `ls --invariant-unknown internal`, "other", nil},
		{"unknown-build-option", `go build --invariant-unknown ./...`, "other", nil},
		{"unknown-test-option", `go test --invariant-unknown ./...`, "other", nil},
		{"unknown-git-option", `git status --invariant-unknown`, "other", nil},
		{"unknown-cd-option", `cd --invariant-unknown internal && cat app.go`, "other", nil},
		{"unknown-wrapper-option", `sh --invariant-unknown -c 'cat app.go'`, "other", nil},
		{"unknown-option-in-wrapper", `sh -c 'rg --invariant-unknown SECRET PATTERN app.go'`, "other", nil},
		{"certain-read-first", `cat safe.go && cat --invariant-unknown SECRET app.go`, "other", nil},
		{"certain-read-last", `cat --invariant-unknown SECRET app.go && cat safe.go`, "other", nil},
	})
}
