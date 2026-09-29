package factory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/synth"
)

// dirBuilder reports where its finished project is, the way synthesis does:
// under the run's out directory, on the watcher's machine.
type dirBuilder struct{ *fakeBuilder }

func (b dirBuilder) Build(ctx context.Context, dir, out string, amend bool) (*synth.Result, error) {
	res, err := b.fakeBuilder.Build(ctx, dir, out, amend)
	if res != nil {
		res.Dir = filepath.Join(out, "result")
	}
	return res, err
}

// jsonStrings collects every string in a decoded JSON value.
func jsonStrings(v any, into *[]string) {
	switch x := v.(type) {
	case string:
		*into = append(*into, x)
	case []any:
		for _, e := range x {
			jsonStrings(e, into)
		}
	case map[string]any:
		for k, e := range x {
			*into = append(*into, k)
			jsonStrings(e, into)
		}
	}
}

func TestSavedBuildResultHoldsNoLocalPath(t *testing.T) {
	r := newRig(t)
	r.f.Builder = dirBuilder{r.build}
	ratified(t, r)
	if len(r.build.built) != 1 {
		t.Fatalf("builds = %v", r.build.built)
	}

	refs := git(t, r.origin, "for-each-ref", "--format=%(refname)", "refs/invariant/runs/1/")
	var ref string
	for _, line := range strings.Split(refs, "\n") {
		if strings.Contains(line, "/build-") {
			ref = line
		}
	}
	if ref == "" {
		t.Fatalf("no build run recorded under refs/invariant/runs/1/: %q", refs)
	}
	raw := git(t, r.origin, "show", ref+":result.json")

	var saved struct {
		Result *synth.Result `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Result == nil {
		t.Fatalf("the saved record holds no result:\n%s", raw)
	}

	work := r.f.Work
	forbidden := []string{work, filepath.ToSlash(work), filepath.Base(filepath.Dir(work))}
	if real, err := filepath.EvalSymlinks(work); err == nil {
		forbidden = append(forbidden, real, filepath.ToSlash(real))
	}
	for _, f := range forbidden {
		if f != "" && strings.Contains(raw, f) {
			t.Errorf("the saved result names the work directory's path %q:\n%s", f, raw)
		}
	}

	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	var all []string
	jsonStrings(v, &all)
	for _, s := range all {
		if filepath.IsAbs(s) || strings.HasPrefix(s, "/") {
			t.Errorf("the saved result holds the absolute path %q:\n%s", s, raw)
		}
	}
}
