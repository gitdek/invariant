package repository

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// #929: a Go part can declare that it builds on macOS too, with the checks
// CI's gate runs for it on a hosted macOS runner: build, vet, test, and
// other checks by name, such as a self-test. Each is a plain argument array.

func macOS929() map[string]any {
	return map[string]any{
		"build": command461("go", "build", "./..."),
		"vet":   command461("go", "vet", "./..."),
		"test":  command461("go", "test", "./..."),
		"checks": []map[string]any{
			{"name": "selftest", "argv": []string{"go", "run", "./cmd/onair", "selftest"}},
		},
	}
}

func TestAGoPartDeclaresItsMacOSChecks929(t *testing.T) {
	app := part461("app", ".", "go")
	app["macos"] = macOS929()
	root := repo461(t, contract461(app, part461("web", "web", "typescript")))
	s := load461(t, root, commit461(t, root))
	config := s.Config()
	want := &MacOS{
		Build:  Command{Argv: []string{"go", "build", "./..."}},
		Vet:    Command{Argv: []string{"go", "vet", "./..."}},
		Test:   Command{Argv: []string{"go", "test", "./..."}},
		Checks: []MacOSCheck{{Name: "selftest", Argv: []string{"go", "run", "./cmd/onair", "selftest"}}},
	}
	if !reflect.DeepEqual(config.Parts[0].MacOS, want) || config.Parts[1].MacOS != nil {
		t.Fatalf("the parts' macOS checks are %+v and %+v, want %+v and none", config.Parts[0].MacOS, config.Parts[1].MacOS, want)
	}
	parts := config.MacOSParts()
	if len(parts) != 1 || parts[0].Name != "app" {
		t.Fatalf("the parts that build on macOS are %+v, want app alone", parts)
	}
	var steps []string
	for _, step := range parts[0].MacOS.Steps() {
		steps = append(steps, step.Name+": "+strings.Join(step.Argv, " "))
	}
	if got := strings.Join(steps, "\n"); got != "build: go build ./...\nvet: go vet ./...\ntest: go test ./...\nselftest: go run ./cmd/onair selftest" {
		t.Errorf("the macOS job runs, in order:\n%s", got)
	}
	// What a caller is given is its own, as the rest of the configuration is.
	config.Parts[0].MacOS.Build.Argv[0] = "true"
	config.Parts[0].MacOS.Checks[0].Argv[0] = "true"
	parts[0].MacOS.Test.Argv[0] = "true"
	if again := s.Config().Parts[0].MacOS; !reflect.DeepEqual(again, want) {
		t.Errorf("changing a returned configuration changed the snapshot's: %+v", again)
	}
	b, err := json.Marshal(s.Config().Parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"macos":{"build":{"argv":["go","build","./..."]},"vet":{"argv":["go","vet","./..."]},"test":{"argv":["go","test","./..."]},"checks":[{"name":"selftest","argv":["go","run","./cmd/onair","selftest"]}]}`) {
		t.Errorf("the part serializes as %s", b)
	}
}

// A contract without macOS checks serializes as it did before them, so the
// execution digests a ratified plan holds don't change.
func TestAContractWithoutMacOSChecksSerializesAsBefore929(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go"), part461("worker", "worker", "python")))
	s := load461(t, root, commit461(t, root))
	b, err := json.Marshal(s.Config())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "macos") || len(s.Config().MacOSParts()) != 0 {
		t.Errorf("a contract without macOS checks has some: %s", b)
	}
}

func TestMacOSChecksArePlainArgumentArrays929(t *testing.T) {
	for _, c := range []struct {
		name  string
		edit  func(macos map[string]any)
		wants string
	}{
		{"a command line", func(m map[string]any) { m["build"] = "go build ./..." }, "must be an object"},
		{"a bare array", func(m map[string]any) { m["vet"] = []string{"go", "vet", "./..."} }, "must be an object"},
		{"a reason in its place", func(m map[string]any) { m["test"] = map[string]any{"not_applicable": "no tests here"} }, "not_applicable doesn't apply"},
		{"a reason beside it", func(m map[string]any) {
			m["test"] = map[string]any{"argv": []string{"go", "test"}, "not_applicable": "no"}
		}, "not_applicable doesn't apply"},
		{"a shell beside it", func(m map[string]any) {
			m["build"] = map[string]any{"argv": []string{"go", "build"}, "shell": "bash"}
		}, `unknown field "shell"`},
		{"no vet", func(m map[string]any) { delete(m, "vet") }, "missing vet"},
		{"no argv", func(m map[string]any) { m["build"] = map[string]any{} }, "missing argv"},
		{"an empty argv", func(m map[string]any) { m["build"] = map[string]any{"argv": []string{}} }, "nonempty argv"},
		{"a null argv", func(m map[string]any) { m["build"] = command461() }, "must not be null"},
		{"a blank executable", func(m map[string]any) { m["build"] = command461(" ", "build") }, "nonblank executable"},
		{"a NUL", func(m map[string]any) { m["build"] = command461("go", "build\x00", "./...") }, "control character"},
		{"a newline", func(m map[string]any) { m["test"] = command461("go", "test", "./...\nrm -rf /") }, "control character"},
		{"a tab", func(m map[string]any) { m["vet"] = command461("go\tvet") }, "control character"},
		{"a placeholder", func(m map[string]any) { m["test"] = command461("go", "test", "-run", "{test}") }, "placeholder"},
		{"a null argument", func(m map[string]any) { m["test"] = map[string]any{"argv": []any{"go", nil}} }, "not null"},
		{"an argv string", func(m map[string]any) { m["test"] = map[string]any{"argv": "go test ./..."} }, "cannot unmarshal"},
		{"null checks", func(m map[string]any) { m["checks"] = nil }, "must not be null"},
		{"checks as an object", func(m map[string]any) { m["checks"] = map[string]any{"selftest": []string{"x"}} }, "cannot unmarshal"},
		{"a check without a name", func(m map[string]any) { m["checks"] = []map[string]any{{"argv": []string{"x"}}} }, "missing name"},
		{"a check with a spaced name", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "self test", "argv": []string{"x"}}}
		}, "must be lowercase"},
		{"a check named for another", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "test", "argv": []string{"x"}}}
		}, "names another"},
		{"two checks by one name", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "selftest", "argv": []string{"x"}}, {"name": "selftest", "argv": []string{"y"}}}
		}, "names another"},
		{"a check with a shell", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "selftest", "argv": []string{"x"}, "shell": "bash"}}
		}, `unknown field "shell"`},
		{"a check with a command line", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "selftest", "argv": "go run ./cmd/onair selftest"}}
		}, "cannot unmarshal"},
		{"a check with an empty argv", func(m map[string]any) {
			m["checks"] = []map[string]any{{"name": "selftest", "argv": []string{}}}
		}, "nonempty argv"},
		{"a runner", func(m map[string]any) { m["runner"] = "macos-26" }, `unknown field "runner"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			macos := macOS929()
			c.edit(macos)
			app := part461("app", ".", "go")
			app["macos"] = macos
			root := repo461(t, contract461(app))
			_, err := Load(t.Context(), root, commit461(t, root))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.wants) {
				t.Fatalf("Load = %v, want ErrInvalid saying %q", err, c.wants)
			}
		})
	}
}

func TestOnlyAGoPartDeclaresMacOSChecks929(t *testing.T) {
	for _, language := range []string{"python", "typescript"} {
		p := part461("app", ".", language)
		p["macos"] = macOS929()
		root := repo461(t, contract461(p))
		_, err := Load(t.Context(), root, commit461(t, root))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "only a Go part") {
			t.Errorf("a %s part's macOS checks: Load = %v", language, err)
		}
	}
	for name, raw := range map[string]string{
		"null":      `"macos":null`,
		"twice":     `"macos":{"build":{"argv":["go","build"]},"vet":{"argv":["go","vet"]},"test":{"argv":["go","test"]}},"macos":{"build":{"argv":["go","build"]},"vet":{"argv":["go","vet"]},"test":{"argv":["go","test"]}}`,
		"an array":  `"macos":[]`,
		"a boolean": `"macos":true`,
	} {
		t.Run(name, func(t *testing.T) {
			root := repo461(t, contract461(part461("app", ".", "go")))
			text := json461(t, contract461(part461("app", ".", "go")))
			text = strings.Replace(text, `"directory":".",`, `"directory":".",`+raw+`,`, 1)
			write461(t, root, contractPath461, text)
			_, err := Load(t.Context(), root, commit461(t, root))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Load = %v, want ErrInvalid", err)
			}
		})
	}
}
