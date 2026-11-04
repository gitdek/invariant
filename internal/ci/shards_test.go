package ci

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// sharded are the packages whose tests run in invariant/verify-tests'
// shards instead of invariant/verify's unit-test step (#345, #570).
var sharded = []string{"/internal/plumbing$", "/internal/verify$"}

func workflow(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "gate.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The unit-test step leaves out exactly the packages the shards run, so no
// test runs twice and none is left out.
func TestUnitStepLeavesOutTheShardedPackages(t *testing.T) {
	line := regexp.MustCompile(`(?m)^\s*go test -timeout 20m \$\(go list \./\.\.\. \| grep -v (.*)\)\s*$`).FindStringSubmatch(workflow(t))
	if line == nil {
		t.Fatal("gate.yml has no unit-test step listing every package but the sharded ones")
	}
	var left []string
	for _, m := range regexp.MustCompile(`-e '([^']+)'`).FindAllStringSubmatch(line[1], -1) {
		left = append(left, m[1])
	}
	slices.Sort(left)
	if !slices.Equal(left, sharded) {
		t.Errorf("the unit-test step leaves out %q, want %q", left, sharded)
	}
}

// invariant/verify-tests runs both packages, in four shards.
func TestVerifyTestsShardBothPackagesInFour(t *testing.T) {
	w := workflow(t)
	start := strings.Index(w, "\n  verify-tests:\n")
	if start < 0 {
		t.Fatal("gate.yml has no verify-tests job")
	}
	job := w[start+1:]
	// The job ends where the next job's key starts, two spaces in.
	if end := regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:\s*$`).FindAllStringIndex(job, 2); len(end) == 2 {
		job = job[:end[1][0]]
	}
	if !strings.Contains(job, "shard: ['1/4', '2/4', '3/4', '4/4']") {
		t.Error("invariant/verify-tests doesn't run in four shards")
	}
	if !strings.Contains(job, `.github/shard-tests.sh "${{ matrix.shard }}" -timeout 20m -- ./internal/verify/ ./internal/plumbing/`) {
		t.Error("invariant/verify-tests doesn't shard internal/verify's and internal/plumbing's tests together")
	}
}

// The four shards together run every top-level test of every package they're
// given exactly once.
func TestFourShardsRunEveryTestOnce(t *testing.T) {
	for _, tool := range []string{"bash", "go", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the shard script needs %s: %v", tool, err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", ".github", "shard-tests.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.invalid/shards\n\ngo 1.21\n")
	var want []string
	for _, pkg := range []string{"one", "two"} {
		var src strings.Builder
		src.WriteString("package " + pkg + "\n\nimport \"testing\"\n")
		for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf"} {
			test := "Test" + strings.ToUpper(pkg[:1]) + pkg[1:] + name
			src.WriteString("\nfunc " + test + "(t *testing.T) { t.Log(\"ran " + pkg + " " + test + "\") }\n")
			want = append(want, pkg+" "+test)
		}
		write(pkg+"/"+pkg+"_test.go", src.String())
	}
	var ran []string
	for _, k := range []string{"1/4", "2/4", "3/4", "4/4"} {
		cmd := exec.Command("bash", script, k, "-v", "--", "./one/", "./two/")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-count=1", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("shard %s: %v\n%s", k, err, out)
		}
		for _, m := range regexp.MustCompile(`ran (one|two) (Test\w+)`).FindAllStringSubmatch(string(out), -1) {
			ran = append(ran, m[1]+" "+m[2])
		}
	}
	slices.Sort(ran)
	slices.Sort(want)
	if !slices.Equal(ran, want) {
		t.Errorf("the four shards ran %q, want each of %q once", ran, want)
	}
}
