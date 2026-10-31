package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// These run the pinned tlapm (#360). It's a linux/amd64 image, emulated on
// an ARM laptop, so they run only with INVARIANT_TLAPS_REAL=1, as CI's
// project job runs the example's proof itself:
//
//	INVARIANT_TLAPS_REAL=1 go test ./internal/verify -run '^TestTLAPSReal' -count=1 -timeout 30m
func tlapsReal(t *testing.T) {
	t.Helper()
	if os.Getenv("INVARIANT_TLAPS_REAL") != "1" {
		t.Skip("set INVARIANT_TLAPS_REAL=1 to run the pinned tlapm")
	}
	if exec.Command("docker", "info").Run() != nil {
		t.Fatal("INVARIANT_TLAPS_REAL=1 needs Docker")
	}
}

// tlapsProject writes the pool's model and a proof into a project of its
// own, as the gate stages one.
func tlapsProject(t *testing.T, model, proof string) *project.Project {
	t.Helper()
	dir := t.TempDir()
	p := &project.Project{Dir: dir,
		Manifest: project.Manifest{Module: ".invariant/specs/ConnectionPool.tla", TLAPS: []string{"NoSharedConnection"}},
		Lock:     project.Lock{Statements: []project.Statement{{Name: "Spec", Kind: project.Spec}, {Name: "NoSharedConnection", Kind: project.Invariant}}},
	}
	for path, text := range map[string]string{p.ModulePath(): model, p.TLAPSProofPath("NoSharedConnection"): proof} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func tlapsProve(t *testing.T, model, proof string) (TLAPSProof, *TLAPSTool) {
	t.Helper()
	proofs, tool, err := proveTLAPS(context.Background(), t.TempDir(), tlapsProject(t, model, proof), model)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 {
		t.Fatalf("proofs = %+v", proofs)
	}
	return proofs[0], tool
}

func TestTLAPSRealProvesTheConnectionPool(t *testing.T) {
	tlapsReal(t)
	got, tool := tlapsProve(t, tlapsPool, tlapsPoolProof)
	if !got.Proved || got.Obligations != 8 || got.Message != "" {
		t.Fatalf("the pool's proof: %+v", got)
	}
	total := 0
	for _, n := range got.Provers {
		total += n
	}
	if total != got.Obligations || got.Provers["ls4"] != 1 {
		t.Errorf("provers %v for %d obligations; the gate's temporal step is LS4's", got.Provers, got.Obligations)
	}
	if got.Model != sha(tlapsPool) || got.Proof != sha(tlapsPoolProof) || got.Theorem != "THEOREM NoSharedConnection_AtEverySize == Spec => []NoSharedConnection" {
		t.Errorf("evidence %+v isn't bound to the model, proof and theorem", got)
	}
	if tool == nil || tool.Version != toolchain.TLAPSVersion || tool.Tarball != "sha256:"+toolchain.TLAPSTarballSHA256 || tool.Recipe != toolchain.TLAPSRecipe() {
		t.Fatalf("tool %+v isn't the pinned TLAPS", tool)
	}
	for _, backend := range []string{"z3", "zenon", "isabelle", "ls4"} {
		if tool.Backends[backend] == "" {
			t.Errorf("no %s version recorded: %v", backend, tool.Backends)
		}
	}
}

func TestTLAPSRealRefusesTheKnownBug(t *testing.T) {
	tlapsReal(t)
	// With AcquireWhenFull a Next step, a full pool lends a connection out
	// twice, and no proof of NoSharedConnection can go through.
	buggy := strings.Replace(tlapsPool, "Next == \\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)",
		"AcquireWhenFull ==\n    \\E c \\in Clients, k \\in Conns :\n        /\\ held[c] = {}\n        /\\ Out = Conns\n        /\\ held' = [held EXCEPT ![c] = {k}]\n\n"+
			"Next == (\\E c \\in Clients : Acquire(c) \\/ Refuse(c) \\/ Release(c)) \\/ AcquireWhenFull", 1)
	proof := strings.Replace(tlapsPoolProof, "Release, Out, Conns", "Release, AcquireWhenFull, Out, Conns", 1)
	got, _ := tlapsProve(t, buggy, proof)
	if got.Proved || !strings.Contains(got.Message, "failed") {
		t.Fatalf("the buggy pool's proof: %+v", got)
	}
}

func TestTLAPSRealRefusesAMissingProof(t *testing.T) {
	tlapsReal(t)
	// A helper lemma with no proof generates no obligation; tlapm --strict
	// reports it, and the gate takes nothing on trust.
	got, _ := tlapsProve(t, tlapsPool, tlapsPoolProof+"\nLEMMA Unproved == TypeOK => NoSharedConnection\n")
	if got.Proved || !strings.Contains(got.Message, "Missing proof") {
		t.Fatalf("a proof with an unproved lemma: %+v", got)
	}
}

func TestTLAPSRealProvesTheInvariantAsItIsNow(t *testing.T) {
	tlapsReal(t)
	// Dropping c # d makes NoSharedConnection false whenever a client holds
	// a connection. The old proof's evidence doesn't carry over: the model's
	// hash moves, and the same proof no longer goes through.
	changed := strings.Replace(tlapsPool, "\\A c, d \\in Clients : c # d => held[c] \\cap held[d] = {}", "\\A c, d \\in Clients : held[c] \\cap held[d] = {}", 1)
	got, _ := tlapsProve(t, changed, tlapsPoolProof)
	if got.Proved || got.Model == sha(tlapsPool) {
		t.Fatalf("a changed invariant: %+v", got)
	}
}

func TestTLAPSRealGatesTheExample(t *testing.T) {
	tlapsReal(t)
	// The example itself, through the whole gate, from a copy.
	dir := filepath.Join(t.TempDir(), "05-connection-pool")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "05-connection-pool"))); err != nil {
		t.Fatal(err)
	}
	tc, err := toolchain.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), dir, "", tc)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed || len(r.TLAPS) != 1 || !r.TLAPS[0].Proved || r.Toolchain.TLAPS == nil {
		t.Fatalf("passed %v, tlaps %+v, tool %+v", r.Passed, r.TLAPS, r.Toolchain.TLAPS)
	}
	if r.Claim() != "proved at every size" {
		t.Errorf("the code's claim changed: %q", r.Claim())
	}
}

func TestTLAPSRealStdlibMatchesTheImage(t *testing.T) {
	tlapsReal(t)
	// The facts the gate names a proof citing are the pinned stdlib's.
	image, err := toolchain.TLAPSImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--entrypoint", "sh", image, "-c",
		`cd /opt/tlapm/lib/tlapm/stdlib && for f in *.tla; do case $f in *_proofs.tla) continue;; esac; grep -oE '^(THEOREM|LEMMA|PROPOSITION|COROLLARY)[[:space:]]+[A-Za-z_][A-Za-z0-9_]*' "$f" | awk -v m="${f%.tla}" '{print m, $2}'; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(lines)
	lines = compactLines(lines)
	if want := strings.TrimSpace(tlapsStdlib); strings.Join(lines, "\n") != want {
		t.Fatalf("the image's stdlib states other facts than tlaps_stdlib.txt:\n%s", strings.Join(lines, "\n"))
	}
}

func compactLines(lines []string) []string {
	var out []string
	for i, l := range lines {
		if i == 0 || l != lines[i-1] {
			out = append(out, l)
		}
	}
	return out
}
