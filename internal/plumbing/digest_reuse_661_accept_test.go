package plumbing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

func nativeBlobCounter661(t *testing.T) func() int {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "reads")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = cat-file ]; then\n    printf 'read\\n' >> " + quote(log) + " || exit 90\n    break\n  fi\ndone\nexec " + quote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		t.Helper()
		b, err := os.ReadFile(log)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "\n")
	}
}

func commitSnapshot661(t *testing.T, root string) *repository.Snapshot {
	t.Helper()
	git468(t, root, "add", "--all")
	git468(t, root, "commit", "-q", "--no-verify", "-m", "changed authority")
	s, err := repository.Load(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativeDigestReusesCapturedBytes661(t *testing.T) {
	root, snapshot, p := n503BindFixture(t)
	// Computed by the unchanged #661 base using n503BindFixture. These pin the
	// versioned canonical JSON, exact CRLF source, sha256 encoding and plan hash.
	const wantDigest = "sha256:3965eae42142386b7842fb4ce0ec57e34f8dba1df915d57451d73327e9ae3d9d"
	const wantHash = "sha256:da12d6428854c9b43eb97c565c132be0bc8e2ad484033f609117ddc0f7d8d783"
	if p.Execution.Digest != wantDigest || p.Hash() != wantHash {
		t.Fatalf("pre-change identity changed: digest=%s hash=%s", p.Execution.Digest, p.Hash())
	}
	count := nativeBlobCounter661(t)
	before := count()
	for i := 0; i < 3; i++ {
		if err := p.ValidateRepository(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(); got != before {
		t.Fatalf("repeated digest validation reread captured blobs: %d -> %d", before, got)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- p.ValidateRepository(t.Context(), snapshot)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count() != before {
		t.Fatal("concurrent digest validation reread immutable inputs")
	}
	data, err := snapshot.ReadFile("shared/input.txt")
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 1
	if err := p.ValidateRepository(t.Context(), snapshot); err != nil {
		t.Fatalf("returned bytes mutated execution authority: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.ValidateRepository(ctx, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cache hid validation cancellation: %v", err)
	}
	if count() != before {
		t.Fatal("a canceled validation started object reads")
	}
	pretty, err := json.MarshalIndent(snapshot.Config(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write468(t, root, repository.Path, string(pretty)+"\n")
	write468(t, root, "unrelated.txt", "different commit, same authority\n")
	equivalent := commitSnapshot661(t, root)
	if equivalent.Commit() == snapshot.Commit() {
		t.Fatal("equivalent fixture did not move the ref")
	}
	if err := p.ValidateRepository(t.Context(), equivalent); err != nil {
		t.Fatalf("equivalent snapshot refused: %v", err)
	}
	fresh := n503Read(t, p, equivalent)
	if fresh.Execution.Digest != wantDigest || fresh.Hash() != wantHash || p.Execution.Base != snapshot.Commit() || fresh.Execution.Base != equivalent.Commit() {
		t.Fatal("equivalent authority changed hashes or rewrote provenance")
	}
	before = count()
	for _, snap := range []*repository.Snapshot{snapshot, equivalent} {
		if err := p.ValidateRepository(t.Context(), snap); err != nil {
			t.Fatal(err)
		}
	}
	if count() != before {
		t.Fatal("equivalent captured snapshots did not retain their own reads")
	}
	for _, change := range []string{"input bytes", "lock bytes", "image", "argv order", "ownership policy"} {
		git468(t, root, "reset", "--hard", equivalent.Commit())
		c := equivalent.Config()
		switch change {
		case "input bytes":
			write468(t, root, "shared/input.txt", "captured dependency input\r\n")
		case "lock bytes":
			write468(t, root, "worker/requirements.lock", files468("python", "worker")["worker/requirements.lock"]+"\n")
		case "image":
			c.Parts[0].Image = "registry.example/python@sha256:" + strings.Repeat("b", 64)
		case "argv order":
			c.Parts[0].Commands["test"].Argv[1], c.Parts[0].Commands["test"].Argv[2] = c.Parts[0].Commands["test"].Argv[2], c.Parts[0].Commands["test"].Argv[1]
		case "ownership policy":
			c.SharedPaths = []repository.SharedPath{{Path: "unrelated.txt", Parts: []string{"worker"}}}
		}
		encoded, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		write468(t, root, repository.Path, string(encoded))
		changed := commitSnapshot661(t, root)
		if p.ValidateRepository(t.Context(), changed) == nil {
			t.Fatalf("%s in a new captured snapshot retained old authority", change)
		}
		if err := p.ValidateRepository(t.Context(), snapshot); err != nil || p.Execution.Digest != wantDigest || p.Hash() != wantHash || p.Execution.Base != snapshot.Commit() {
			t.Fatalf("%s reinterpreted the old snapshot: %v", change, err)
		}
	}
	// Preserve real set ordering and exclusions, with more than one member.
	worker, web := n503Part("python", "worker"), n503Part("typescript", "web")
	worker.Inputs = []string{"shared/input.txt", "shared/settings.json"}
	other, _, original := fixture484(t, []repository.Part{worker, web}, func(c *repository.Config) {
		c.AgentRules, c.CurrentState = []string{"AGENTS.md", "STATE.md"}, []string{"STATE.md", "AGENTS.md"}
		c.TrustedPaths = []string{"tools/", "guard/"}
		c.SharedPaths = []repository.SharedPath{
			{Path: "shared/input.txt", Parts: []string{"worker", "web"}},
			{Path: "shared/settings.json", Parts: []string{"worker", "web"}},
		}
	})
	bound := n503Read(t, n503Plan(t, "pytest", "worker", "worker/test_order.py", "test_order.py::test_ready", "def test_ready():\n    assert False\n"), original)
	c := original.Config()
	slices.Reverse(c.Parts)
	slices.Reverse(c.AgentRules)
	slices.Reverse(c.CurrentState)
	slices.Reverse(c.TrustedPaths)
	slices.Reverse(c.SharedPaths)
	for i := range c.Parts {
		slices.Reverse(c.Parts[i].Inputs)
		slices.Reverse(c.Parts[i].Lockfiles)
	}
	for i := range c.SharedPaths {
		slices.Reverse(c.SharedPaths[i].Parts)
	}
	encoded, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write468(t, other, repository.Path, string(encoded))
	for _, file := range append([]string{"AGENTS.md", "STATE.md"}, append(slices.Clone(web.Lockfiles), web.Inputs...)...) {
		write468(t, other, file, "changed excluded bytes\r\n")
	}
	reordered := commitSnapshot661(t, other)
	if err := bound.ValidateRepository(t.Context(), reordered); err != nil {
		t.Fatalf("set order or excluded bytes invalidated authority: %v", err)
	}
	if next := n503Read(t, bound, reordered); next.Hash() != bound.Hash() || next.Execution.Digest != bound.Execution.Digest {
		t.Fatal("canonical declarations or exclusions changed identity")
	}
	lock := Lock{Plan: *p, Ratified: Ratification{Issue: 661, Proposal: wantHash}}
	locked, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip, err := ReadLock(locked); err != nil || roundTrip.Plan.Hash() != wantHash {
		t.Fatalf("unchanged lock requires migration: %v", err)
	}
}
