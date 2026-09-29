package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gitdek/invariant/internal/decisions"
	"github.com/gitdek/invariant/internal/mcp"
)

const decisionsUsage = `invariant decisions keeps every project's decisions in one graph (D-0096).

Recording:
  invariant decisions decide [-door two-way|one-way] [-status decided|proposed|ratified] [-who WHO]
                             [-record SLUG] [-refines ID]... [-cites ID]... [-reopens ID]... TEXT
                                    record a new decision, and print its ID
  invariant decisions ratify -by @PERSON ID
                                    record a person's ratification; only when that person asks
  invariant decisions supersede -with ID ID
                                    record that a later decision replaces one
  invariant decisions link ID refines|cites|reopens|supersedes ID
                                    record an edge between two decisions

Asking:
  invariant decisions show ID       a decision, and every edge to and from it
  invariant decisions dependents ID everything that rests on a decision: what reopening it touches
  invariant decisions implementers ID
                                    the code that implements it, or a decision resting on it
  invariant decisions grounds ID    the decisions it rests on
  invariant decisions search WORDS  decisions whose words hold every one of WORDS
  invariant decisions sql QUERY     read-only SQL over the node and edge tables

Keeping it whole:
  invariant decisions rebuild [-all] [DIR...]
                                    replace a project in the store with what its checkout holds
  invariant decisions check [-base REF] [DIR]
                                    rebuild from the checkout alone, and check the graph (CI runs this)
  invariant decisions log [DIR]     bring decisions/log.md's table up to date with the journal
  invariant decisions import [DIR]  carry decisions/log.md's table into the journal, once

Every command takes -C DIR, the checkout (default .), and -store FILE (default
the store on this machine). A project is named after its repository's origin,
or -project NAME. An ID is short within the project, as D-0096, or qualified,
as copythis-ad/D-0001.
`

// idList is a flag that can be given more than once.
type idList []string

func (l *idList) String() string     { return strings.Join(*l, ",") }
func (l *idList) Set(v string) error { *l = append(*l, v); return nil }

// decisionFlags are the flags every decisions command takes.
type decisionFlags struct {
	dir, store, project *string
	asJSON              *bool
}

func newDecisionFlags(name string) (*flag.FlagSet, decisionFlags) {
	fs := flag.NewFlagSet("decisions "+name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, decisionsUsage) }
	return fs, decisionFlags{
		dir:     fs.String("C", ".", "the checkout"),
		store:   fs.String("store", "", "the store (default: the one on this machine)"),
		project: fs.String("project", "", "the project's name (default: its repository's, from origin)"),
		asJSON:  fs.Bool("json", false, "print JSON"),
	}
}

var projectName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// checkout finds the repository dir is in, and names its project after the
// repository its origin points at.
func checkout(ctx context.Context, dir, project string) (decisions.Repo, error) {
	top, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return decisions.Repo{}, fmt.Errorf("%s isn't in a git checkout", dir)
	}
	root := strings.TrimSpace(string(top))
	if project == "" {
		project = filepath.Base(root)
		if url, err := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin").Output(); err == nil {
			u := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(string(url)), "/"), ".git")
			if i := strings.LastIndexAny(u, "/:"); i >= 0 && i < len(u)-1 {
				project = u[i+1:]
			}
		}
	}
	if !projectName.MatchString(project) {
		return decisions.Repo{}, fmt.Errorf("%q can't name a project; give one with -project", project)
	}
	return decisions.Repo{Name: project, Dir: root}, nil
}

func (f decisionFlags) open() (*decisions.Store, error) {
	path := *f.store
	if path == "" {
		var err error
		if path, err = decisions.DefaultPath(); err != nil {
			return nil, err
		}
	}
	return decisions.Open(path)
}

// ensure rebuilds a project the store has never held, so the first question
// about it has answers.
func ensure(s *decisions.Store, repo decisions.Repo) error {
	held, err := s.Checkouts()
	if err != nil {
		return err
	}
	for _, r := range held {
		if r.Name == repo.Name {
			return nil
		}
	}
	return s.Rebuild([]decisions.Repo{repo})
}

func decisionsCmd(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, decisionsUsage)
		return 2
	}
	sub, args := args[0], args[1:]
	var err error
	code := 0
	switch sub {
	case "decide":
		err = decideCmd(ctx, args)
	case "ratify", "supersede", "link":
		err = changeCmd(ctx, sub, args)
	case "show", "dependents", "implementers", "grounds", "search":
		err = askCmd(ctx, sub, args)
	case "sql":
		err = sqlCmd(ctx, args)
	case "rebuild":
		err = rebuildCmd(ctx, args)
	case "check":
		code, err = checkDecisionsCmd(ctx, args)
	case "log":
		err = logCmd(ctx, args)
	case "import":
		err = importCmd(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "invariant decisions: unknown command %q\n\n%s", sub, decisionsUsage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant decisions:", err)
		return 1
	}
	return code
}

// afterWrite brings decisions/log.md up to date with a write, where the
// checkout has one.
func afterWrite(repo decisions.Repo) error {
	if _, err := os.Stat(filepath.Join(repo.Dir, "decisions", "log.md")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	changed, err := decisions.WriteLog(repo)
	if err == nil && changed {
		fmt.Fprintln(os.Stderr, "Updated decisions/log.md.")
	}
	return err
}

func decideCmd(ctx context.Context, args []string) error {
	fs, f := newDecisionFlags("decide")
	door := fs.String("door", "two-way", "one-way or two-way")
	status := fs.String("status", "decided", "decided, proposed or ratified")
	who := fs.String("who", "agent", "who made the decision: a person, such as @gitdek, or agent")
	by := fs.String("by", "agent", "who is writing it down")
	record := fs.String("record", "", "a one-way door's record, by its slug, for decisions/D-NNNN-slug.md")
	var refines, cites, reopens idList
	fs.Var(&refines, "refines", "a decision this one refines (repeatable)")
	fs.Var(&cites, "cites", "a decision this one cites (repeatable)")
	fs.Var(&reopens, "reopens", "a decision this one reopens (repeatable)")
	fs.Parse(args)
	text := strings.Join(fs.Args(), " ")
	if text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		text = string(b)
	}
	repo, err := checkout(ctx, *f.dir, *f.project)
	if err != nil {
		return err
	}
	var edges []decisions.Edge
	for _, set := range []struct {
		typ string
		ids idList
	}{{decisions.Refines, refines}, {decisions.Cites, cites}, {decisions.Reopens, reopens}} {
		for _, id := range set.ids {
			edges = append(edges, decisions.Edge{Type: set.typ, To: id})
		}
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	defer s.Close()
	if err := ensure(s, repo); err != nil {
		return err
	}
	id, err := s.Decide(repo, decisions.NewDecision{Door: *door, Status: *status, Who: *who, Text: text, Record: *record, Edges: edges}, *by)
	if err != nil {
		return err
	}
	fmt.Println(id)
	if *record != "" {
		fmt.Fprintf(os.Stderr, "Write its record in decisions/%s-%s.md.\n", id, *record)
	}
	return afterWrite(repo)
}

func changeCmd(ctx context.Context, sub string, args []string) error {
	fs, f := newDecisionFlags(sub)
	by := fs.String("by", "agent", "who is writing it down; for ratify, the person ratifying")
	with := fs.String("with", "", "for supersede: the decision that replaces it")
	fs.Parse(args)
	repo, err := checkout(ctx, *f.dir, *f.project)
	if err != nil {
		return err
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	defer s.Close()
	if err := ensure(s, repo); err != nil {
		return err
	}
	switch {
	case sub == "ratify" && fs.NArg() == 1:
		if !strings.HasPrefix(*by, "@") {
			return errors.New("ratify names the person ratifying, as -by @gitdek. Ratifying is theirs: record it only when they ask")
		}
		err = s.Ratify(repo, fs.Arg(0), *by)
	case sub == "supersede" && fs.NArg() == 1 && *with != "":
		err = s.Supersede(repo, fs.Arg(0), *with, *by)
	case sub == "link" && fs.NArg() == 3:
		err = s.Link(repo, fs.Arg(0), decisions.Edge{Type: fs.Arg(1), To: fs.Arg(2)}, *by)
	default:
		fmt.Fprint(os.Stderr, decisionsUsage)
		return errors.New("see the usage above")
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Recorded in decisions/journal/%s.jsonl.\n", strings.TrimPrefix(fs.Arg(0), repo.Name+"/"))
	return afterWrite(repo)
}

func askCmd(ctx context.Context, sub string, args []string) error {
	fs, f := newDecisionFlags(sub)
	fs.Parse(args)
	if fs.NArg() == 0 || (sub != "search" && fs.NArg() != 1) {
		fmt.Fprint(os.Stderr, decisionsUsage)
		return errors.New("see the usage above")
	}
	repo, err := checkout(ctx, *f.dir, *f.project)
	if err != nil {
		return err
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	defer s.Close()
	if err := ensure(s, repo); err != nil {
		return err
	}
	if sub == "search" {
		nodes, err := s.Search(strings.Join(fs.Args(), " "))
		if err != nil {
			return err
		}
		return printNodes(nodes, *f.asJSON)
	}
	id, err := decisions.Qualify(repo.Name, fs.Arg(0))
	if err != nil {
		return err
	}
	node, err := s.Get(id)
	if err != nil {
		return err
	}
	var nodes []decisions.Node
	switch sub {
	case "show":
		near, err := s.Neighbors(id)
		if err != nil {
			return err
		}
		if *f.asJSON {
			return printJSON(map[string]any{"node": node, "edges": near})
		}
		printShow(node, near)
		return nil
	case "dependents":
		nodes, err = s.Dependents(id)
	case "implementers":
		nodes, err = s.Implementers(id)
	case "grounds":
		nodes, err = s.Grounds(id)
	}
	if err != nil {
		return err
	}
	return printNodes(nodes, *f.asJSON)
}

func sqlCmd(ctx context.Context, args []string) error {
	fs, f := newDecisionFlags("sql")
	timeout := fs.Duration("timeout", 10*time.Second, "stop a query that runs longer")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprint(os.Stderr, decisionsUsage)
		return errors.New("see the usage above")
	}
	repo, err := checkout(ctx, *f.dir, *f.project)
	if err != nil {
		return err
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	err = ensure(s, repo)
	s.Close()
	if err != nil {
		return err
	}
	ro, err := decisions.OpenReadOnly(s.Path)
	if err != nil {
		return err
	}
	defer ro.Close()
	cols, rows, err := ro.Query(strings.Join(fs.Args(), " "), *timeout)
	if err != nil {
		return err
	}
	if *f.asJSON {
		out := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			m := map[string]any{}
			for i, c := range cols {
				m[c] = row[i]
			}
			out = append(out, m)
		}
		return printJSON(out)
	}
	fmt.Println(strings.Join(cols, "\t"))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = fmt.Sprint(v)
		}
		fmt.Println(strings.Join(cells, "\t"))
	}
	return nil
}

func rebuildCmd(ctx context.Context, args []string) error {
	fs, f := newDecisionFlags("rebuild")
	all := fs.Bool("all", false, "every project the store holds, each from the checkout it was last rebuilt from")
	fs.Parse(args)
	dirs := fs.Args()
	if len(dirs) == 0 && !*all {
		dirs = []string{*f.dir}
	}
	var repos []decisions.Repo
	for _, d := range dirs {
		project := ""
		if len(dirs) == 1 {
			project = *f.project
		}
		repo, err := checkout(ctx, d, project)
		if err != nil {
			return err
		}
		repos = append(repos, repo)
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	defer s.Close()
	if *all {
		held, err := s.Checkouts()
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, r := range repos {
			have[r.Name] = true
		}
		for _, r := range held {
			if have[r.Name] {
				continue
			}
			if _, err := os.Stat(r.Dir); err != nil {
				fmt.Fprintf(os.Stderr, "Skipped %s: its checkout at %s is gone. Rebuild it from another one.\n", r.Name, r.Dir)
				continue
			}
			repos = append(repos, r)
		}
	}
	if err := s.Rebuild(repos); err != nil {
		return err
	}
	for _, r := range repos {
		n, e, err := s.Count(r.Name)
		if err != nil {
			return err
		}
		fmt.Printf("Rebuilt %s from %s: %d decisions, %d edges.\n", r.Name, r.Dir, n, e)
	}
	return nil
}

func checkDecisionsCmd(ctx context.Context, args []string) (int, error) {
	fs, f := newDecisionFlags("check")
	base := fs.String("base", "", "a git ref the journal must only have grown from, such as origin/main")
	fs.Parse(args)
	dir := *f.dir
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	repo, err := checkout(ctx, dir, *f.project)
	if err != nil {
		return 0, err
	}
	// A store of its own, from this checkout alone, as CI has.
	tmp, err := os.MkdirTemp("", "invariant-decisions-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	s, err := decisions.Open(filepath.Join(tmp, "decisions.db"))
	if err != nil {
		return 0, err
	}
	defer s.Close()
	if err := s.Rebuild([]decisions.Repo{repo}); err != nil {
		fmt.Printf("❌ The journal doesn't rebuild: %v\n", err)
		return 1, nil
	}
	problems, err := s.Check([]decisions.Repo{repo})
	if err != nil {
		return 0, err
	}
	if *base != "" {
		more, err := decisions.Grown(ctx, repo, *base)
		if err != nil {
			return 0, err
		}
		problems = append(problems, more...)
	}
	if len(problems) > 0 {
		fmt.Printf("❌ The decision graph has %d problem%s:\n- %s\n", len(problems), plural(len(problems)), strings.Join(problems, "\n- "))
		return 1, nil
	}
	n, e, err := s.Count(repo.Name)
	if err != nil {
		return 0, err
	}
	fmt.Printf("✅ %s's decision graph holds: %d decisions and %d edges, every cited decision exists, the SPEC rests on none that's superseded, and decisions/log.md is the journal's view.\n", repo.Name, n, e)
	return 0, nil
}

func logCmd(ctx context.Context, args []string) error {
	fs, f := newDecisionFlags("log")
	fs.Parse(args)
	dir := *f.dir
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	repo, err := checkout(ctx, dir, *f.project)
	if err != nil {
		return err
	}
	changed, err := decisions.WriteLog(repo)
	if err != nil {
		return err
	}
	if changed {
		fmt.Println("Updated decisions/log.md from the journal.")
	} else {
		fmt.Println("decisions/log.md is already the journal's view.")
	}
	return nil
}

func importCmd(ctx context.Context, args []string) error {
	fs, f := newDecisionFlags("import")
	fs.Parse(args)
	dir := *f.dir
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	repo, err := checkout(ctx, dir, *f.project)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(repo.JournalDir()); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already has a journal, so its log was carried over already", repo.Name)
	}
	log, err := os.ReadFile(filepath.Join(repo.Dir, "decisions", "log.md"))
	if err != nil {
		return err
	}
	events, err := decisions.ParseLog(string(log))
	if err != nil {
		return err
	}
	if err := decisions.Import(repo, events); err != nil {
		return err
	}
	if _, err := decisions.WriteLog(repo); err != nil {
		return err
	}
	s, err := f.open()
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Rebuild([]decisions.Repo{repo}); err != nil {
		return err
	}
	fmt.Printf("Carried %d decisions from decisions/log.md into decisions/journal/, and marked the log's table as the journal's view.\n", len(events))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// label is what a list shows beside a node: a decision's status, or the
// kind of text that cites one.
func label(n decisions.Node) string {
	if n.Kind == "decision" {
		return n.Status
	}
	if n.Kind == "" {
		return "not loaded"
	}
	return n.Kind
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func printNodes(nodes []decisions.Node, asJSON bool) error {
	if asJSON {
		if nodes == nil {
			nodes = []decisions.Node{}
		}
		return printJSON(nodes)
	}
	if len(nodes) == 0 {
		fmt.Println("Nothing.")
		return nil
	}
	for _, n := range nodes {
		fmt.Printf("%-46s %-10s %s\n", n.ID, label(n), clip(n.Text, 110))
	}
	return nil
}

func printShow(n decisions.Node, near []decisions.Neighbor) {
	head := []string{n.ID}
	for _, v := range []string{n.Door, n.Status, n.Date, n.Who} {
		if v != "" {
			head = append(head, v)
		}
	}
	fmt.Println(strings.Join(head, " · "))
	fmt.Println(n.Text)
	if n.Record != "" {
		fmt.Printf("Record: decisions/%s\n", n.Record)
	}
	for _, out := range []bool{true, false} {
		var lines []string
		for _, nb := range near {
			if nb.Out == out {
				lines = append(lines, fmt.Sprintf("  %-11s %-46s %s", nb.Type, nb.Node.ID, clip(nb.Node.Text, 90)))
			}
		}
		if len(lines) == 0 {
			continue
		}
		if out {
			fmt.Println("\nFrom it:")
		} else {
			fmt.Println("\nTo it:")
		}
		fmt.Println(strings.Join(lines, "\n"))
	}
}

// decisionsMCP serves the decision graph's tools to a coding agent over MCP,
// as the checkout at dir has the graph (D-0096).
func decisionsMCP(ctx context.Context, dir, store string, write bool) int {
	repo, err := checkout(ctx, dir, "")
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	f := decisionFlags{store: &store}
	shared, err := f.open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	defer shared.Close()
	session, err := decisions.NewSession(shared, repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 2
	}
	defer session.Close()
	for _, s := range session.Skipped {
		fmt.Fprintln(os.Stderr, "invariant: left out of the graph:", s)
	}
	server := mcp.Server{Name: "invariant-decisions", Version: "0.1", Tools: session.Tools(write)}
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "invariant:", err)
		return 1
	}
	return 0
}
