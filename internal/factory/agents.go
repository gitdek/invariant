package factory

import (
	"fmt"
	"sort"
	"strings"
)

// Coding agents (#173). An issue picks the coding agent that drafts and
// builds it with an Agent: line, such as Agent: codex, else the manifest of
// the project it changes picks one, else it gets the watcher's own. Every
// agent run for the issue is that agent's: the draft of its statements, its
// plan or its plan of issues, and the build of its project or plan, whose
// review the builder makes with the same agent (D-0087, D-0108). The watcher
// never runs another in its place.

// claudeCode is the agent a watcher runs when it names none of its own.
const claudeCode = "claude-code"

// Runners are what a coding agent runs for the watcher: Formalizer drafts
// an issue's statements, its plan or its plan of issues, Builder builds a
// ratified project, and Plumbing a ratified plan. A nil Plumbing builds no
// plans, as the watcher's own may not.
type Runners struct {
	Formalizer Formalizer
	Builder    Builder
	Plumbing   PlanBuilder
}

// agentLine reads an issue's Agent: line, such as Agent: codex, as its
// Project: line is read: outside code blocks, the first one counting.
func agentLine(body string) string {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if name, agent, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "agent") {
			return strings.ToLower(strings.Trim(strings.TrimSpace(agent), "`"))
		}
	}
	return ""
}

// ownAgent names the watcher's own agent, which its Formalizer, Builder and
// Plumbing run.
func (f *Factory) ownAgent() string {
	if f.Agent == "" {
		return claudeCode
	}
	return f.Agent
}

// agentOf picks an issue's agent: the one its own Agent: line names, else
// named, the one the manifest of the project it changes names, else the
// watcher's own.
func (f *Factory) agentOf(body, named string) string {
	if agent := agentLine(body); agent != "" {
		return agent
	}
	if named != "" {
		return named
	}
	return f.ownAgent()
}

// runners are an agent's runners, and whether the watcher can run it.
func (f *Factory) runners(agent string) (Runners, bool) {
	if agent == f.ownAgent() {
		return Runners{Formalizer: f.Formalizer, Builder: f.Builder, Plumbing: f.Plumbing}, true
	}
	r, ok := f.Agents[agent]
	return r, ok
}

// runnable names every agent the watcher can run: its own, then the others
// in order.
func (f *Factory) runnable() []string {
	var others []string
	for name := range f.Agents {
		if name != f.ownAgent() {
			others = append(others, name)
		}
	}
	sort.Strings(others)
	return append([]string{f.ownAgent()}, others...)
}

// buildAgent is the agent that builds a ratified proposal, with its runners
// and whether the watcher can run it: the issue's, as its Agent: line picks
// it now, else as the manifest of its project was ratified, else the
// watcher's own. A plan has no manifest.
func (f *Factory) buildAgent(t Thread, m Marker) (string, Runners, bool) {
	named := ""
	if p := m.Proposal; p != nil && !isPlan(m.Project) {
		named = p.Manifest().Agent
	}
	agent := f.agentOf(t.Issue.Body, named)
	run, ok := f.runners(agent)
	return agent, run, ok
}

// cantRun is why a build fails before any run is recorded when the watcher
// can't run its issue's agent: a build never runs another.
func cantRun(agent string) error {
	return fmt.Errorf("this watcher can't run %s, this issue's coding agent, and no other agent builds it", agent)
}

// unrunnableComment answers an issue whose agent the watcher can't run, or
// doesn't know, before anything is drafted, as plumbingElsewhereComment
// answers plumbing on another repository (D-0121). can names every agent
// the watcher can run, its own first.
func unrunnableComment(agent string, can []string, m Marker) string {
	return post("not one I can take", fmt.Sprintf("This issue's coding agent is `%s`, and this watcher can't run it. It can run %s. "+
		"An issue's `Agent:` line picks its agent, else the manifest of the project it changes does, else it gets the watcher's own.\n\n"+
		"So I haven't drafted anything, and I've left this issue for a person. To go on, name an agent this watcher can run in the issue's `Agent:` line, "+
		"such as `Agent: %s`, then comment `/invariant revise`.", agent, list(can), can[0]), m)
}
