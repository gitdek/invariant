package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/tlc"
)

// Larger is the check one size past the bounds (D-0068): TLC and agreement
// again, with every bound that has a next size one bigger. A project whose
// explorer names its bounds has its code split from its environment, so its
// code must agree there too: when TLC finishes one size larger, a code that
// disagrees fails the gate, because it carries a bound. When TLC can't
// finish, or finds a problem in the model there, nothing is claimed or
// failed.
type Larger struct {
	Passed     bool              `json:"passed"`
	Required   bool              `json:"required,omitempty"` // TLC finished one size larger, so the code must agree
	Bounds     map[string]string `json:"bounds"`
	States     int64             `json:"states,omitempty"`
	Depth      int               `json:"depth,omitempty"`
	WantStates int64             `json:"want_states,omitempty"`
	WantDepth  int               `json:"want_depth,omitempty"`
	Message    string            `json:"message,omitempty"`
}

// EverySize says whether a receipt may claim the code at every size: it's
// proved, and it agrees with the model one size past the bounds too.
func (r *Report) EverySize() bool {
	return r.Code != nil && r.Code.Passed && r.Larger != nil && r.Larger.Passed
}

// Claim is what the report says of the code, in the receipt's words:
// "proved", "proved at every size", or "tested against the model".
func (r *Report) Claim() string {
	if r.EverySize() {
		return r.Assurance + " at every size"
	}
	return r.Assurance
}

var (
	numberBound = regexp.MustCompile(`^\d+$`)
	setBound    = regexp.MustCompile(`^\{\s*([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\s*\}$`)
	numbered    = regexp.MustCompile(`^([A-Za-z_]+?)(\d+)$`)
)

// largerBounds is every bound one size larger, where it has a next size: a
// number plus one, or a set of numbered model values, such as {p1, p2}, with
// the next one added. sizes are the new sizes of those bounds, for the
// explorer's constants.
func largerBounds(bounds map[string]string) (larger map[string]string, sizes map[string]int) {
	larger, sizes = map[string]string{}, map[string]int{}
	for name, v := range bounds {
		v = strings.TrimSpace(v)
		larger[name] = v
		if numberBound.MatchString(v) {
			n, err := strconv.Atoi(v)
			if err == nil && n < 1<<20 {
				larger[name], sizes[name] = strconv.Itoa(n+1), n+1
			}
			continue
		}
		m := setBound.FindStringSubmatch(v)
		if m == nil {
			continue
		}
		elems := strings.Split(m[1], ",")
		prefix, next := "", 0
		for i, e := range elems {
			e = strings.TrimSpace(e)
			elems[i] = e
			p := numbered.FindStringSubmatch(e)
			if p == nil || (i > 0 && p[1] != prefix) {
				prefix = ""
				break
			}
			prefix = p[1]
			if k, _ := strconv.Atoi(p[2]); k >= next {
				next = k + 1
			}
		}
		if prefix == "" {
			continue
		}
		elems = append(elems, prefix+strconv.Itoa(next))
		larger[name], sizes[name] = "{"+strings.Join(elems, ", ")+"}", len(elems)
	}
	return larger, sizes
}

// explorerFile is where a Go project's explorer lives.
const explorerFile = "explore.go"

// boundConst finds a bound's constant in an explorer or a driver, to change
// its value: "\tCapacity = 2" in Go, "const Capacity = 2;" in TypeScript,
// and "Capacity = 2" in Python.
func boundConst(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^(\s*(?:export\s+)?(?:const\s+|let\s+)?` + regexp.QuoteMeta(name) +
		`\s*(?::\s*number\s*)?=\s*)(\d+)(\s*;?\s*(?://.*|#.*)?)$`)
}

// declaresBounds says whether an explorer declares a constant for each of
// the bounds, named after it, so the gate can make it one size larger.
func declaresBounds(src []byte, sizes map[string]int) bool {
	if len(sizes) == 0 {
		return false
	}
	for name := range sizes {
		if len(boundConst(name).FindAllIndex(src, -1)) != 1 {
			return false
		}
	}
	return true
}

// largerExplorer is an explorer's source with its bounds one size larger.
func largerExplorer(src []byte, sizes map[string]int) []byte {
	names := make([]string, 0, len(sizes))
	for n := range sizes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		src = boundConst(name).ReplaceAll(src, []byte("${1}"+strconv.Itoa(sizes[name])+"${3}"))
	}
	return src
}

// agreementOnly runs just the agreement step, in the same sandbox.
const agreementOnly = `cd /src
cp /agree/zz_invariant_agreement_test.go "./$PKG/" && go test -count=1 -run '^TestInvariantAgreement$' -v "./$PKG" ; echo "@@invariant agree=$?"
`

// exploreLarger explores the package again with its explorer one size
// larger.
func (g Go) exploreLarger(ctx context.Context, projectDir, pkg string, sizes map[string]int) (Exploration, error) {
	root, err := moduleRoot(projectDir, pkg)
	if err != nil {
		return Exploration{}, err
	}
	rel, err := filepath.Rel(root, pkg)
	if err != nil {
		return Exploration{}, err
	}
	name, err := packageName(pkg)
	if err != nil {
		return Exploration{}, err
	}
	work, err := os.MkdirTemp("", "invariant-go-larger-")
	if err != nil {
		return Exploration{}, err
	}
	defer os.RemoveAll(work)
	src, agreeDir := filepath.Join(work, "src"), filepath.Join(work, "agree")
	if err := copyTree(root, src); err != nil {
		return Exploration{}, err
	}
	explorer := filepath.Join(src, rel, explorerFile)
	b, err := os.ReadFile(explorer)
	if err != nil {
		return Exploration{}, err
	}
	if err := os.WriteFile(explorer, largerExplorer(b, sizes), 0o644); err != nil {
		return Exploration{}, err
	}
	if err := os.MkdirAll(agreeDir, 0o755); err != nil {
		return Exploration{}, err
	}
	if err := os.WriteFile(filepath.Join(agreeDir, "zz_invariant_agreement_test.go"), []byte(fmt.Sprintf(agreementTest, name)), 0o644); err != nil {
		return Exploration{}, err
	}
	if err := pulled(ctx, g.GoImage); err != nil {
		return Exploration{}, err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"-e", "GOTOOLCHAIN=local", "-e", "GOFLAGS=-mod=readonly", "-e", "GOCACHE=/tmp/gocache", "-e", "HOME=/tmp",
		"-e", "CGO_ENABLED=0", "-e", "PKG="+filepath.ToSlash(rel),
		"-v", src+":/src", "-v", agreeDir+":/agree:ro", "-w", "/src", g.GoImage, "sh", "-c", agreementOnly)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Exploration{}, fmt.Errorf("running the Go sandbox: %w", err)
		}
	}
	s, err := splitMarkers(out.String(), 1)
	if err != nil {
		return Exploration{}, err
	}
	if m := explored.FindStringSubmatch(s[0].out); m != nil && s[0].code == 0 {
		e := Exploration{OK: true}
		e.States, _ = strconv.ParseInt(m[1], 10, 64)
		e.Depth, _ = strconv.Atoi(m[2])
		return e, nil
	}
	return Exploration{Message: lastLines(s[0].out, 20)}, nil
}

// countScript runs a TypeScript or Python conformance driver in count mode:
// it explores as always, and writes only how many states it reached, and
// how deep its search went, to $INVARIANT_COUNT.
const countScript = `cd /src
timeout %d %s "$DRIVER" > /out/driver.log 2>&1 ; echo "@@invariant count=$?"
`

// countTimeout caps a driver's count one size larger. A driver explores
// more slowly than TLC, so it gets twice TLC's time. One that runs out
// claims nothing and fails nothing, as TLC doesn't when it runs out.
const countTimeout = 2 * largerTimeout

// boundsFile is where a TypeScript or Python project's bounds live: at the
// top of its conformance driver, or of its explorer for a Python core.
func boundsFile(p *project.Project) string {
	if p.Manifest.Language == "python" {
		if _, err := os.Stat(filepath.Join(p.CodeDir(), "explore.py")); err == nil {
			return filepath.Join(p.Manifest.Code, "explore.py")
		}
	}
	return p.Manifest.Conformance
}

// countLarger runs an exhaustive driver again with its bounds one size
// larger, in count mode, and returns what it reached (D-0068, D-0076).
func countLarger(ctx context.Context, image, runner string, p *project.Project, sizes map[string]int) (Exploration, error) {
	work, err := os.MkdirTemp("", "invariant-larger-")
	if err != nil {
		return Exploration{}, err
	}
	defer os.RemoveAll(work)
	src, out := filepath.Join(work, "src"), filepath.Join(work, "out")
	runtime := filepath.Join(work, "runtime")
	if err := copyTree(p.Dir, src); err != nil {
		return Exploration{}, err
	}
	bounds := filepath.Join(src, boundsFile(p))
	b, err := os.ReadFile(bounds)
	if err != nil {
		return Exploration{}, err
	}
	if err := os.WriteFile(bounds, largerExplorer(b, sizes), 0o644); err != nil {
		return Exploration{}, err
	}
	if err := writeNaginiRuntime(runtime); err != nil {
		return Exploration{}, err
	}
	if err := os.MkdirAll(out, 0o777); err != nil {
		return Exploration{}, err
	}
	if err := pulled(ctx, image); err != nil {
		return Exploration{}, err
	}
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"-e", "HOME=/tmp", "-e", "PYTHONHASHSEED=0", "-e", "PYTHONPATH=/runtime", "-v", runtime+":/runtime:ro",
		"-e", "DRIVER="+filepath.ToSlash(p.Manifest.Conformance), "-e", "INVARIANT_COUNT=/out/count.json",
		"-v", src+":/src", "-v", out+":/out", "-w", "/src", image, "sh", "-c", fmt.Sprintf(countScript, int(countTimeout.Seconds()), runner))
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return Exploration{}, fmt.Errorf("running the sandbox: %w", err)
		}
	}
	s, err := splitMarkers(buf.String(), 1)
	if err != nil {
		return Exploration{}, err
	}
	log, _ := os.ReadFile(filepath.Join(out, "driver.log"))
	raw, readErr := os.ReadFile(filepath.Join(out, "count.json"))
	var count struct {
		States int64 `json:"states"`
		Depth  int   `json:"depth"`
	}
	switch {
	case s[0].code == 124 || s[0].code == 143:
		return Exploration{Unfinished: true, Message: fmt.Sprintf("the driver didn't finish counting one size larger within %s", countTimeout)}, nil
	case s[0].code != 0:
		return Exploration{Message: "the driver failed one size larger:\n" + lastLines(string(log), 20)}, nil
	case readErr != nil || json.Unmarshal(raw, &count) != nil || count.States <= 0:
		return Exploration{Message: "the driver wrote no count to $INVARIANT_COUNT, as {\"states\": N, \"depth\": D}"}, nil
	}
	return Exploration{OK: true, States: count.States, Depth: count.Depth}, nil
}

// largerTimeout caps TLC's run one size larger. A model that grows past it
// keeps the claim it has within the bounds.
const largerTimeout = 3 * time.Minute

// compareLarger says whether the code agrees with the model one size past
// the bounds.
func compareLarger(bounds map[string]string, model *tlc.Result, code *Exploration, problem string) *Larger {
	l := &Larger{Bounds: bounds, Message: problem}
	switch {
	case model == nil:
	case model.Outcome != tlc.Passed:
		l.Message = "TLC found a problem one size larger: " + describe(*model)
	case code != nil && code.Unfinished:
		l.Message = code.Message
	case code == nil || !code.OK:
		l.Required = true
		l.Message = "couldn't explore the code one size larger"
		if code != nil && code.Message != "" {
			l.Message += ":\n" + code.Message
		}
	default:
		l.Required = true
		l.States, l.Depth, l.WantStates, l.WantDepth = code.States, code.Depth, model.DistinctStates, model.Depth
		l.Passed = code.States == model.DistinctStates && code.Depth == model.Depth
		if !l.Passed {
			l.Message = fmt.Sprintf("one size larger, the code reaches %d states in %d levels, and the model reaches %d in %d", code.States, code.Depth, model.DistinctStates, model.Depth)
		}
	}
	return l
}
