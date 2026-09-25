// Package tlc runs the TLC model checker and reads its results.
package tlc

import (
	"regexp"
	"strconv"
	"strings"
)

// Outcome is how a TLC run ended.
type Outcome string

const (
	Passed   Outcome = "passed"   // every reachable state satisfies every invariant
	Violated Outcome = "violated" // an invariant fails in some reachable state
	Deadlock Outcome = "deadlock" // some reachable state has no successor
	Failed   Outcome = "error"    // TLC couldn't finish: a parse error, a bad config, a crash
)

// Result is what one TLC run established.
type Result struct {
	Version         string
	Outcome         Outcome
	Invariant       string // the violated invariant, when Outcome is Violated
	Message         string // TLC's own explanation, when Outcome is Failed
	StatesGenerated int64
	DistinctStates  int64
	Depth           int
	Trace           []State // the counterexample, when Outcome is Violated or Deadlock
	ExitCode        int
}

// State is one step of a counterexample trace. Values are TLA+ text as TLC
// printed it; ParseValue turns them into data.
type State struct {
	Index  int
	Action string // "Initial predicate" for the first state
	Vars   []Var
}

// Var is one variable's value in a State.
type Var struct {
	Name  string
	Value string
}

// TLC's -tool mode wraps each message in markers carrying a message code.
var (
	message   = regexp.MustCompile(`(?s)@!@!@STARTMSG (\d+):\d+ @!@!@\n(.*?)@!@!@ENDMSG \d+ @!@!@`)
	violated  = regexp.MustCompile(`Invariant (\S+) is violated`)
	stats     = regexp.MustCompile(`([\d,]+) states generated, ([\d,]+) distinct states found`)
	depth     = regexp.MustCompile(`search is (\d+)`)
	stateHead = regexp.MustCompile(`^(\d+): <(.*)>$`)
)

// Message codes from tlc2.output.EC.
const (
	codeVersion       = 2262
	codeSuccess       = 2193
	codeViolated      = 2110
	codeDeadlock      = 2114
	codeBehavior      = 2121
	codeTraceState    = 2217
	codeStats         = 2199
	codeDepth         = 2194
	codeFinished      = 2186
	codeStarting      = 2185
	codeSANYStart     = 2220
	codeSANYEnd       = 2219
	codeInitComputing = 2189
	codeInitDone      = 2190
	codeProgress      = 2200
	codeRunning       = 2187
	codeOutdegree     = 2268
)

// informational codes never signal a failure on their own.
var informational = map[int]bool{
	codeVersion: true, codeSuccess: true, codeBehavior: true, codeTraceState: true,
	codeStats: true, codeDepth: true, codeFinished: true, codeStarting: true,
	codeSANYStart: true, codeSANYEnd: true, codeInitComputing: true, codeInitDone: true,
	codeProgress: true, codeRunning: true, codeOutdegree: true,
}

// Parse reads the output of `tlc2.TLC -tool` and its exit code.
func Parse(out string, exitCode int) Result {
	r := Result{ExitCode: exitCode}
	var success, deadlock bool
	var problems []string
	for _, m := range message.FindAllStringSubmatch(out, -1) {
		code, _ := strconv.Atoi(m[1])
		body := strings.TrimRight(m[2], "\n")
		switch code {
		case codeVersion:
			r.Version = strings.TrimSpace(body)
		case codeSuccess:
			success = true
		case codeViolated:
			if v := violated.FindStringSubmatch(body); v != nil {
				r.Invariant = v[1]
			}
		case codeDeadlock:
			deadlock = true
		case codeTraceState:
			if s, ok := parseState(body); ok {
				r.Trace = append(r.Trace, s)
			}
		case codeStats:
			if s := stats.FindStringSubmatch(body); s != nil {
				r.StatesGenerated = atoi64(s[1])
				r.DistinctStates = atoi64(s[2])
			}
		case codeDepth:
			if d := depth.FindStringSubmatch(body); d != nil {
				r.Depth = int(atoi64(d[1]))
			}
		default:
			if !informational[code] {
				problems = append(problems, strings.TrimSpace(body))
			}
		}
	}
	switch {
	case r.Invariant != "":
		r.Outcome = Violated
	case deadlock:
		r.Outcome = Deadlock
	case success && exitCode == 0:
		r.Outcome = Passed
	default:
		r.Outcome = Failed
		r.Message = strings.Join(problems, "\n")
		if r.Message == "" {
			r.Message = tail(out, 20)
		}
	}
	return r
}

// parseState reads one trace state: a header line such as
// `2: <RMPrepare line 64, col 5 to line 67, col 40 of module TwoPhase>`,
// then one `/\ name = value` line per variable. Long values wrap onto
// indented continuation lines.
func parseState(body string) (State, bool) {
	lines := strings.Split(body, "\n")
	head := stateHead.FindStringSubmatch(strings.TrimSpace(lines[0]))
	if head == nil {
		return State{}, false
	}
	s := State{Index: int(atoi64(head[1])), Action: strings.SplitN(head[2], " line ", 2)[0]}
	for _, line := range lines[1:] {
		trimmed := strings.TrimPrefix(line, `/\ `)
		name, value, isVar := strings.Cut(trimmed, " = ")
		switch {
		case isVar && (trimmed != line || len(s.Vars) == 0) && isIdent(name):
			s.Vars = append(s.Vars, Var{Name: name, Value: value})
		case len(s.Vars) > 0 && strings.TrimSpace(line) != "":
			last := &s.Vars[len(s.Vars)-1]
			last.Value += " " + strings.TrimSpace(line)
		}
	}
	return s, true
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
