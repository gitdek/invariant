package verify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// #284: JSON absence and null are not the empty string, false, [] or {}.
// Mutate raw objects rather than typed report fields: a struct round trip
// would silently repair precisely the omissions this regression exercises.
// Later array entries ensure validation covers every row, not just the first.
func TestPythonDependenciesRequireEveryPipReportField(t *testing.T) {
	objects := []struct {
		name         string
		fields       []string
		selectObject func(map[string]any) map[string]any
	}{
		{"report", []string{"schema", "pip_version", "root", "files", "includes", "requirements", "options", "errors"}, func(r map[string]any) map[string]any { return r }},
		{"files/2", []string{"path", "constraint"}, func(r map[string]any) map[string]any {
			return r["files"].([]any)[2].(map[string]any)
		}},
		{"includes/1", []string{"source", "target", "value", "constraint"}, func(r map[string]any) map[string]any {
			return r["includes"].([]any)[1].(map[string]any)
		}},
		{"requirements/1", []string{"source", "line", "name", "extras", "specifier", "marker", "hashes", "constraint", "url", "link", "path", "archive", "editable", "options"}, func(r map[string]any) map[string]any {
			return r["requirements"].([]any)[1].(map[string]any)
		}},
		{"options/1", []string{"source", "line", "name", "value"}, func(r map[string]any) map[string]any {
			r["options"] = pyDepsReportOptionRows()
			return r["options"].([]any)[1].(map[string]any)
		}},
		{"requirements/0/options/1", []string{"source", "line", "name", "value"}, func(r map[string]any) map[string]any {
			row := r["requirements"].([]any)[0].(map[string]any)
			row["options"] = pyDepsReportOptionRows()
			return row["options"].([]any)[1].(map[string]any)
		}},
		{"errors/1", []string{"source", "line", "type", "message"}, func(r map[string]any) map[string]any {
			r["errors"] = []any{
				map[string]any{"source": "deps/application.txt", "line": 1, "type": "InstallationError", "message": "acceptance input refusal"},
				map[string]any{"source": "deps/constraints.txt", "line": 1, "type": "InvalidRequirement", "message": "acceptance constraint refusal"},
			}
			return r["errors"].([]any)[1].(map[string]any)
		}},
	}
	for _, cache := range []string{"fresh", "cached"} {
		for _, object := range objects {
			for _, field := range object.fields {
				for _, absence := range []string{"omitted", "null"} {
					t.Run(cache+"/"+object.name+"/"+field+"/"+absence, func(t *testing.T) {
						t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
						pyDepsFakeDocker(t)
						root := pyDepsFixture(t)
						complete := pyDepsAuthorityFixtureReport()
						pyDepsSetParserReport(t, complete)
						if cache == "cached" {
							pyDepsBuild(t, root)
						}
						pyDepsClearReportCalls(t)
						raw := pyDepsReportObject(t, complete)
						row := object.selectObject(raw)
						if _, present := row[field]; !present {
							t.Fatalf("fixture does not contain required field %s", field)
						}
						if absence == "omitted" {
							delete(row, field)
						} else {
							row[field] = nil
						}
						pyDepsSetParserReport(t, raw)
						pyDepsAssertIncompleteReportRefused(t, root, []string{field})
					})
				}
			}
		}
	}
}

// Use the exact reviewed requirement in each file role. It must never become
// an unconditional or unpinned package when either semantic field disappears,
// including when valid evidence for this same source was already cached.
func TestPythonDependenciesMissingPipFieldsCannotDropPinsOrMarkers(t *testing.T) {
	for _, filename := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
		for _, cache := range []string{"fresh", "cached"} {
			for _, fields := range [][]string{{"specifier"}, {"marker"}, {"specifier", "marker"}} {
				for _, absence := range []string{"omitted", "null"} {
					t.Run(filename+"/"+cache+"/"+strings.Join(fields, "+")+"/"+absence, func(t *testing.T) {
						t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
						pyDepsFakeDocker(t)
						root := pyDepsFixture(t)
						body := pyDepsFiles(t, root)[filename]
						pyDepsWrite(t, root, filename, "packaging==24.2; python_version < '0'\n"+body)
						complete, err := pyDepsFixtureParserReport(root)
						if err != nil {
							t.Fatal(err)
						}
						pyDepsSetParserRawReport(t, complete)
						if cache == "cached" {
							env := pyDepsBuild(t, root)
							canonical := pyDepsAssertCanonicalStaging(t, root, env, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
							name := "requirements.txt"
							if filename == "deps/constraints.txt" {
								name = "constraints.txt"
							}
							if !strings.Contains(canonical[name], "packaging==24.2; python_version < '0'\n") {
								t.Fatalf("complete control report lost pins or marker: %q", canonical[name])
							}
						}
						pyDepsClearReportCalls(t)
						var raw map[string]any
						if err := json.Unmarshal(complete, &raw); err != nil {
							t.Fatal(err)
						}
						found := false
						for _, item := range raw["requirements"].([]any) {
							row := item.(map[string]any)
							if row["source"] != filename || row["line"] != float64(1) {
								continue
							}
							if row["specifier"] != "==24.2" || row["marker"] != "python_version < '0'" {
								t.Fatalf("regression fixture does not preserve both pip fields: %#v", row)
							}
							found = true
							for _, field := range fields {
								if absence == "omitted" {
									delete(row, field)
								} else {
									row[field] = nil
								}
							}
						}
						if !found {
							t.Fatal("missing regression requirement in report fixture")
						}
						pyDepsSetParserReport(t, raw)
						pyDepsAssertIncompleteReportRefused(t, root, fields)
					})
				}
			}
		}
	}
}

// Presence checks must retain legitimate empty values: an unpinned named
// requirement without a marker, extras, hashes, links or options is supported.
// With no includes, the report's include collection is explicitly [] too.
func TestPythonDependenciesAcceptExplicitEmptyPipReportValues(t *testing.T) {
	t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
	pyDepsFakeDocker(t)
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "requirements.txt", "packaging\n")
	report := pyDepsAuthorityReport{
		Schema: 1, PipVersion: "26.2.1", Root: "requirements.txt",
		Files:    []pyDepsAuthorityFile{{"requirements.txt", false}},
		Includes: []pyDepsAuthorityInclude{},
		Requirements: []pyDepsAuthorityRequirement{{
			Source: "requirements.txt", Line: 1, Name: "packaging",
			Extras: []string{}, Hashes: map[string][]string{}, Options: []pyDepsAuthorityOption{},
		}},
		Options: []pyDepsAuthorityOption{}, Errors: []pyDepsAuthorityError{},
	}
	pyDepsSetParserReport(t, report)
	before := pyDepsFiles(t, root)
	env := pyDepsBuild(t, root)
	canonical := pyDepsAssertCanonicalStaging(t, root, env, []string{"requirements.txt"})
	pyDepsAssertCanonicalLines(t, canonical["requirements.txt"], []string{"packaging"})
	if canonical["constraints.txt"] != "" {
		t.Fatalf("empty constraint closure gained a requirement: %q", canonical["constraints.txt"])
	}
	if recipe, err := toolchain.PythonDepsRecipe(root); err != nil || recipe != env.Recipe {
		t.Fatalf("explicit empty values failed recipe selection: recipe=%q error=%v", recipe, err)
	}
	if again := pyDepsBuild(t, root); !reflect.DeepEqual(again, env) || pyDepsBuildCount(t) != 1 {
		t.Fatalf("complete report with empty values did not reuse evidence: first=%#v again=%#v", env, again)
	}
	pyDepsAssertParserCalls(t, root)
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("accepting explicit empty report fields rewrote application files")
	}
}

func pyDepsReportOptionRows() []any {
	return []any{
		map[string]any{"source": "deps/application.txt", "line": 1, "name": "--no-index", "value": ""},
		map[string]any{"source": "deps/application.txt", "line": 1, "name": "--pre", "value": ""},
	}
}

func pyDepsReportObject(t *testing.T, report pyDepsAuthorityReport) map[string]any {
	t.Helper()
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func pyDepsClearReportCalls(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "calls.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pyDepsAssertIncompleteReportRefused(t *testing.T, root string, fields []string) {
	t.Helper()
	before := pyDepsFiles(t, root)
	assertError := func(api string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s accepted an incomplete pip report", api)
			return
		}
		message := strings.ToLower(err.Error())
		identified := false
		for _, field := range fields {
			identified = identified || strings.Contains(message, field)
		}
		structural := false
		for _, term := range []string{"missing", "omitted", "absent", "null", "required", "incomplete"} {
			structural = structural || strings.Contains(message, term)
		}
		// Options and errors are themselves forbidden in a successful report.
		// Their structural cases must fail because of the incomplete field,
		// rather than passing this test merely by refusing the complete row.
		if !identified || !structural {
			t.Errorf("%s did not identify an incomplete field %v: %v", api, fields, err)
		}
	}
	recipe, err := toolchain.PythonDepsRecipe(root)
	assertError("recipe API", err)
	if recipe != "" {
		t.Errorf("incomplete report selected recipe %q", recipe)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	env, err := toolchain.PythonDepsImage(ctx, root)
	assertError("image API", err)
	if env != nil {
		t.Errorf("incomplete report selected dependency environment %#v", env)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	assertError("Python check", err)
	if b.Passed || len(evidence.Traces) != 0 {
		t.Errorf("incomplete report produced application evidence: build=%#v evidence=%#v", b, evidence)
	}
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "build" || call.Kind == "application" || call.Kind == "evidence" {
			t.Errorf("incomplete parsing reached installation, cached evidence or execution: %#v", call)
		}
	}
	pyDepsAssertParserCalls(t, root)
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("rejecting an incomplete pip report changed application files")
	}
}
