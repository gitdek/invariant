package plumbing

import (
	"testing"
)

func TestGoCompilerPreparedDocker604(t *testing.T) {
	// Only Docker is replaced by #591's unchanged daemon-free boundary. The
	// production preparer, Docker process helper, compiler and runtime all run.
	boundary := newDocker591(t)
	c := docker591Case{"api", "nested", "TestReady", compilerSource604(`if Future604()!=7 {t.Fatal("API value")};t.Log("prepared API executed 604")`)}
	f := newDocker591Repository(t, boundary, []docker591Case{c}, true)
	base, problem, err := f.run(t.Context(), true)
	if err != nil || problem != "" {
		t.Fatalf("prepared Go compiler did not supply attributable evidence: %v %s\n%s", err, problem, base.Summary())
	}
	compilerRecord604(t, base, f.plan, f.plan.Tests[0], "Future604")
	write468(t, f.candidate, "api/nested/value.go", "package fixture\nconst Value=7\nfunc Future604() int{return 7}\n")
	candidate, _, err := f.run(t.Context(), false)
	if err != nil || !candidate.Passed() {
		t.Fatalf("prepared candidate did not execute unchanged acceptance: %v\n%s", err, candidate.Summary())
	}
	docker591Result(t, candidate, f.plan.Tests[0], "passed", "prepared API executed 604")
	if f.plan.Sources[c.file()] != c.source {
		t.Fatal("prepared compiler changed ratified bytes")
	}
	docker591RoundTrip(t, base)
	docker591RoundTrip(t, candidate)
	docker591CheckStaging(t, f.runner)
	boundary.clean()
}
