package verify

import (
	"bytes"
	"context"
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

// boundConst finds a bound's constant in an explorer, such as
// "\tCapacity = 2", to change its value.
func boundConst(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^(\s*` + regexp.QuoteMeta(name) + `\s*=\s*)(\d+)(\s*(?://.*)?)$`)
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
