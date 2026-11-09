package dashboard

import (
	"fmt"
	"html/template"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/github"
	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/verify"
)

// Evidence pages wear the dashboard's look and link to what they came from on
// GitHub (#786). They run no script under their sandbox, so the look is all
// HTML and CSS, in the dashboard's palette and its fonts.

// evidenceHead is every evidence page's head after its title.
const evidenceHead = `<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600&family=Space+Grotesk:wght@400;500;600;700&display=swap">
<style>
:root{--bg:#090c11;--card:rgba(13,18,24,.72);--card-solid:#111821;--line:#1b2430;--line2:#283341;--ink:#e6edf3;--ink2:#a8b3bf;--ink3:#6b7785;--accent:#38d9a9;--people:#f5c451;--ci:#6fa8ff;--bug:#ff6b6b;--sans:"Space Grotesk",ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;--mono:"JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color-scheme:dark}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;min-height:100vh;color:var(--ink);font:16px/1.55 var(--sans);background:radial-gradient(1100px 560px at 8% -12%,rgba(56,217,169,.09),transparent 60%),radial-gradient(900px 520px at 100% -6%,rgba(111,168,255,.08),transparent 60%),var(--bg)}
a{color:inherit}
main{max-width:1180px;margin:0 auto;padding:0 clamp(16px,4vw,40px) 72px}
.top{display:flex;align-items:center;gap:14px;flex-wrap:wrap;padding:22px 0;margin-bottom:28px;border-bottom:1px solid var(--line)}
.brand{display:flex;align-items:center;gap:10px;font-weight:700;font-size:18px;letter-spacing:-.01em;text-decoration:none}
.brand svg{width:26px;height:26px}
.crumb{color:var(--ink3);font:500 13px/1.4 var(--mono);overflow-wrap:anywhere}
.back{margin-left:auto;font-size:14px;color:var(--ink2);text-decoration:none;border:1px solid var(--line2);border-radius:999px;padding:7px 14px;transition:border-color .15s,color .15s}
.back:hover{border-color:var(--accent);color:var(--ink)}
.hero{background:var(--card);border:1px solid var(--line);border-radius:22px;padding:30px clamp(18px,3vw,34px);box-shadow:0 30px 80px rgba(0,0,0,.35)}
.eyebrow{margin:0;color:var(--ink3);font:500 13px/1.4 var(--mono);letter-spacing:.02em;overflow-wrap:anywhere}
h1{margin:8px 0 16px;font-size:clamp(28px,4.2vw,42px);line-height:1.08;letter-spacing:-.025em}
.chips{display:flex;flex-wrap:wrap;gap:8px;margin:0 0 16px;padding:0;list-style:none}
.chip{display:inline-flex;align-items:center;padding:7px 11px;border-radius:999px;border:1px solid var(--line2);color:var(--ink2);background:rgba(255,255,255,.02);font:600 12px/1 var(--mono);letter-spacing:.06em;text-transform:uppercase}
.chip.holds{color:var(--accent);border-color:rgba(56,217,169,.45);background:rgba(56,217,169,.1)}
.chip.can{color:var(--ci);border-color:rgba(111,168,255,.45);background:rgba(111,168,255,.1)}
.chip.people{color:var(--people);border-color:rgba(245,196,81,.45);background:rgba(245,196,81,.1)}
.chip.bug{color:var(--bug);border-color:rgba(255,107,107,.45);background:rgba(255,107,107,.1)}
.chip.ts{color:#8fb6ff;border-color:transparent;background:rgba(49,120,198,.2)}
.chip.py{color:#ffd76a;border-color:transparent;background:rgba(55,118,171,.22)}
.chip.go{color:#7fd8f0;border-color:transparent;background:rgba(0,173,216,.14)}
.lede{margin:0;max-width:78ch;color:var(--ink2)}
.links{display:flex;flex-wrap:wrap;gap:10px;margin:22px 0 0;padding:0;list-style:none}
.link{display:flex;align-items:center;gap:10px;min-height:44px;padding:9px 14px;border-radius:12px;border:1px solid var(--line2);background:var(--card-solid);text-decoration:none;transition:border-color .15s,transform .15s}
a.link:hover{border-color:var(--accent);transform:translateY(-1px)}
.link svg{flex:none;width:16px;height:16px;fill:var(--ink2)}
a.link:hover svg{fill:var(--accent)}
.link b{display:block;font:600 14px/1.25 var(--sans)}
.link small{display:block;color:var(--ink3);font:12px/1.35 var(--mono);overflow-wrap:anywhere}
.link.text{opacity:.75}
.chart{margin:30px 0 0;text-align:center}
.chart img{display:block;width:100%;height:auto}
.chart img.poster{display:inline-block;width:auto;max-width:100%;border-radius:28px;box-shadow:0 30px 90px rgba(0,0,0,.55)}
.chart p{margin:14px 0 0}
.chart a{color:var(--ci);font-size:14px;text-underline-offset:3px}
section{margin-top:40px}
h2{margin:0 0 4px;font-size:22px;letter-spacing:-.015em}
h3.group{margin:24px 0 2px;font-size:16px;letter-spacing:-.01em}
.note{margin:0 0 14px;color:var(--ink3);font-size:14px}
.rules{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,330px),1fr));gap:12px;margin:0;padding:0;list-style:none}
.rule{padding:14px 16px;border:1px solid var(--line);border-left:3px solid var(--line2);border-radius:14px;background:var(--card)}
.rule.holds{border-left-color:var(--accent)}
.rule.can{border-left-color:var(--ci)}
.rule.bug{border-left-color:var(--bug)}
.rule h4{display:flex;flex-wrap:wrap;align-items:center;gap:8px;margin:0 0 6px;font:600 14px/1.3 var(--mono);overflow-wrap:anywhere}
.rule h4 small{padding:3px 7px;border:1px solid var(--line2);border-radius:999px;color:var(--ink3);font:600 10px/1 var(--mono);letter-spacing:.08em;text-transform:uppercase}
.rule p{margin:0;color:var(--ink2);font-size:15px}
.rule .found{margin-top:10px;color:var(--accent);font:500 12px/1.4 var(--mono)}
.rule.can .found{color:var(--ci)}
.rule.bug .found{color:var(--bug)}
.rule .found.missed{color:var(--bug)}
.rule .pin{margin-top:8px;color:var(--ink3);font:11px/1.4 var(--mono);overflow-wrap:anywhere}
.bounds{display:flex;flex-wrap:wrap;gap:8px;margin:0;padding:0;list-style:none}
.bound{padding:8px 12px;border:1px solid var(--line2);border-radius:10px;background:var(--card-solid);font:13px/1.4 var(--mono);overflow-wrap:anywhere}
.bound b{color:var(--ink);font-weight:600}
.bound span{color:var(--ink2)}
details{margin-top:40px;padding:12px 16px;border:1px solid var(--line);border-radius:14px;background:var(--card)}
summary{cursor:pointer;color:var(--ink2);font-weight:600}
pre{margin:12px 0 0;white-space:pre-wrap;overflow-wrap:anywhere;color:var(--ink2);font:12px/1.5 var(--mono)}
code{font-family:var(--mono);overflow-wrap:anywhere}
.meta{margin-top:24px;color:var(--ink3);font:12px/1.7 var(--mono);overflow-wrap:anywhere}
.meta code{color:var(--ink2)}
.foot{margin-top:10px;color:var(--ink3);font-size:13px}
@media (prefers-reduced-motion:reduce){*{transition:none!important}a.link:hover{transform:none}}
</style>`

// evidenceParts are the pieces every evidence page shares: its top bar, its
// hero with links out, its rules and its bounds. Each page's template text
// carries them, so its URL changes with them.
const evidenceParts = `{{define "top"}}<header class="top"><a class="brand" href="{{.Back}}" aria-label="Invariant: {{.RepoShort}} on the dashboard"><svg viewBox="-30 -30 60 60" aria-hidden="true"><circle r="27" fill="#0E1116"/><path d="M19.78,-7.05A21,21 0 1 1 -3.78,-20.66" fill="none" stroke="#E6EDF3" stroke-width="5" stroke-linecap="round"/><circle cx="14.85" cy="-14.85" r="5" fill="#E6EDF3"/><circle r="8.8" fill="#38D9A9"/></svg>Invariant</a><span class="crumb">{{.RepoShort}} · {{.Kind}}</span><a class="back" href="{{.Back}}">Back to {{.RepoShort}} on the dashboard</a></header>{{end}}
{{define "hero"}}<p class="eyebrow">{{.Eyebrow}}</p><h1>{{.Title}}</h1>{{if .Chips}}<ul class="chips">{{range .Chips}}<li class="chip {{.Tone}}">{{.Text}}</li>{{end}}</ul>{{end}}{{end}}
{{define "links"}}{{if .Links}}<ul class="links" aria-label="On GitHub">{{range .Links}}<li>{{if .Href}}<a class="link" href="{{.Href}}">{{icon .Kind}}<span><b>{{.Label}}</b>{{if .Detail}}<small>{{.Detail}}</small>{{end}}</span></a>{{else}}<span class="link text">{{icon .Kind}}<span><b>{{.Label}}</b>{{if .Detail}}<small>{{.Detail}}</small>{{end}}</span></span>{{end}}</li>{{end}}</ul>{{end}}{{end}}
{{define "rules"}}{{range .Groups}}<h3 class="group">{{.Title}}</h3><p class="note">{{.Note}}</p><ul class="rules">{{range .Rules}}<li class="rule {{$.Tone .Kind}}"><h4>{{.Name}}<small>{{.Kind}}</small></h4><p>{{.Says}}</p>{{if .Found}}<div class="found{{if .Missed}} missed{{end}}">{{.Found}}</div>{{end}}{{if .Pin}}<div class="pin" title="sha256:{{.Pin}}">pin {{short .Pin}}</div>{{end}}</li>{{end}}</ul>{{end}}{{end}}
{{define "bounds"}}<ul class="bounds">{{range .Bounds}}<li class="bound"><b>{{.Name}}</b> <span>= {{.Value}}</span></li>{{end}}</ul>{{end}}`

type evidenceView struct {
	Kind, Back, RepoShort, Eyebrow, Title string
	Chips                                 []evidenceChip
	Links                                 []evidenceLink
	Groups                                []evidenceGroup
	Bounds                                []evidenceBound
}

type evidenceChip struct{ Text, Tone string }

// An evidenceLink without Href names something whose place on GitHub isn't
// certain, so the page shows it as text rather than a link that might miss.
type evidenceLink struct{ Kind, Label, Detail, Href string }

type evidenceGroup struct {
	Title, Note string
	Rules       []evidenceRule
}

type evidenceRule struct {
	Name, Kind, Says, Found, Pin string
	Missed                       bool
}

type evidenceBound struct{ Name, Value string }

// Tone colors a rule's card by what it promises.
func (evidenceView) Tone(kind string) string {
	switch kind {
	case project.Invariant, project.Property:
		return "holds"
	case project.Witness:
		return "can"
	case project.Bug:
		return "bug"
	}
	return ""
}

// The icons are GitHub's Octicons (MIT), drawn inline: the page loads no
// images but its own.
var evidenceIcons = map[string]string{
	"repo":   `M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z`,
	"issue":  `M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3ZM8 0a8 8 0 1 1 0 16A8 8 0 0 1 8 0ZM1.5 8a6.5 6.5 0 1 0 13 0 6.5 6.5 0 0 0-13 0Z`,
	"check":  `M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm1.5 0a6.5 6.5 0 1 0 13 0 6.5 6.5 0 0 0-13 0Zm10.28-1.72-4.5 4.5a.75.75 0 0 1-1.06 0l-2-2a.751.751 0 0 1 .018-1.042.751.751 0 0 1 1.042-.018l1.47 1.47 3.97-3.97a.751.751 0 0 1 1.042.018.751.751 0 0 1 .018 1.042Z`,
	"file":   `M2 1.75C2 .784 2.784 0 3.75 0h6.586c.464 0 .909.184 1.237.513l2.914 2.914c.329.328.513.773.513 1.237v9.586A1.75 1.75 0 0 1 13.25 16h-9.5A1.75 1.75 0 0 1 2 14.25Zm1.75-.25a.25.25 0 0 0-.25.25v12.5c0 .138.112.25.25.25h9.5a.25.25 0 0 0 .25-.25V6h-2.75A1.75 1.75 0 0 1 9 4.25V1.5Zm6.75.062V4.25c0 .138.112.25.25.25h2.688l-.011-.013-2.914-2.914-.013-.011Z`,
	"folder": `M1.75 1A1.75 1.75 0 0 0 0 2.75v10.5C0 14.216.784 15 1.75 15h12.5A1.75 1.75 0 0 0 16 13.25v-8.5A1.75 1.75 0 0 0 14.25 3H7.5a.25.25 0 0 1-.2-.1l-.9-1.2C6.07 1.26 5.55 1 5 1H1.75Z`,
	"commit": `M11.93 8.5a4.002 4.002 0 0 1-7.86 0H.75a.75.75 0 0 1 0-1.5h3.32a4.002 4.002 0 0 1 7.86 0h3.32a.75.75 0 0 1 0 1.5Zm-1.43-.75a2.5 2.5 0 1 0-5 0 2.5 2.5 0 0 0 5 0Z`,
}

var evidenceFuncs = template.FuncMap{
	// short is a pin's first twelve digits, as commands quote a hash.
	"short": func(pin string) string { return pin[:min(len(pin), 12)] },
	"icon": func(kind string) template.HTML {
		d, ok := evidenceIcons[kind]
		if !ok {
			d = evidenceIcons["file"]
		}
		return template.HTML(`<svg viewBox="0 0 16 16" aria-hidden="true"><path d="` + d + `"/></svg>`)
	},
}

var (
	githubRepoName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	githubCommit   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	decisionName   = regexp.MustCompile(`^D-[0-9]{4,}$`)
)

// onGitHub is a page on GitHub for a well-formed repository name, or "".
func onGitHub(repo string, parts ...string) string {
	if !githubRepoName.MatchString(repo) {
		return ""
	}
	return strings.Join(append([]string{"https://github.com/" + repo}, parts...), "/")
}

// githubPath escapes a clean path inside a repository for a GitHub URL, or
// returns "" for one that isn't.
func githubPath(p string) string {
	if p == "" || path.Clean(p) != p || path.IsAbs(p) || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, `\`) {
		return ""
	}
	segments := strings.Split(p, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// githubAt is a file ("blob") or folder ("tree") at a commit, or "".
func githubAt(repo, kind, commit, p string) string {
	q := githubPath(p)
	if q == "" || !githubCommit.MatchString(commit) {
		return ""
	}
	return onGitHub(repo, kind, commit, q)
}

// dashboardView is the repository's own view on the dashboard (#773).
func dashboardView(repo string) string { return "/?repo=" + url.QueryEscape(path.Base(repo)) }

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return strings.ToUpper(string(r)) + s[n:]
}

func languageChip(language string) evidenceChip {
	switch language {
	case "typescript":
		return evidenceChip{"TypeScript", "ts"}
	case "python":
		return evidenceChip{"Python", "py"}
	case "", "go":
		return evidenceChip{"Go", "go"}
	}
	return evidenceChip{language, ""}
}

// assuranceChip says what the code earned in the words the dashboard uses.
func assuranceChip(assurance string) evidenceChip {
	switch assurance {
	case "proved":
		return evidenceChip{"Code proved", "holds"}
	case "tested in every state":
		return evidenceChip{"Code tested in every state", "holds"}
	case "tested":
		return evidenceChip{"Code tested", "can"}
	case "":
		return evidenceChip{"Model only", ""}
	}
	return evidenceChip{"Code " + assurance, ""}
}

func boundsOf(bounds map[string]string) []evidenceBound {
	out := make([]evidenceBound, 0, len(bounds))
	for name, value := range bounds {
		out = append(out, evidenceBound{name, value})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func counted(n int64, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

// rulesOf groups statements by what they promise, with what a receipt found
// for each when there is one.
func rulesOf(statements []project.Statement, r *verify.Report) []evidenceGroup {
	groups := []evidenceGroup{
		{Title: "What must always hold", Note: "TLC checks these in every state the model reaches within the bounds."},
		{Title: "What can happen", Note: "Each must be reachable, so the rules can't hold by the model doing nothing."},
		{Title: "Bugs it must catch", Note: "Each known bug must break a rule above, which shows the rules can tell."},
		{Title: "How it runs", Note: "The model's behavior, and the steps it must eventually take."},
	}
	for _, s := range statements {
		rule := evidenceRule{Name: s.Name, Kind: s.Kind, Says: s.Says, Pin: strings.TrimPrefix(s.SHA256, "sha256:")}
		g := 3
		switch s.Kind {
		case project.Invariant, project.Property:
			g = 0
		case project.Witness:
			g = 1
		case project.Bug:
			g = 2
		}
		if r != nil {
			rule.Found, rule.Missed = foundFor(s, r)
		}
		groups[g].Rules = append(groups[g].Rules, rule)
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g.Rules) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// foundFor is what a receipt says the check found for one statement.
func foundFor(s project.Statement, r *verify.Report) (string, bool) {
	switch s.Kind {
	case project.Invariant:
		if r.Design.Violated == s.Name {
			return "Broken: TLC found a state where it fails", true
		}
		if r.Design.Passed && r.Design.DistinctStates > 0 {
			return "Held in all " + counted(r.Design.DistinctStates, "checked state"), false
		}
	case project.Property:
		for _, p := range r.Properties {
			if p.Name == s.Name {
				if p.Holds {
					return "Holds under the model's fairness", false
				}
				return "Doesn't hold: TLC found a behavior that never gets there", true
			}
		}
	case project.Witness:
		for _, w := range r.Witnesses {
			if w.Name == s.Name {
				if w.Reached {
					return "Reached in " + counted(int64(w.Steps), "step"), false
				}
				return "Not reached", true
			}
		}
	case project.Bug:
		for _, b := range r.Bugs {
			if b.Name == s.Name {
				if b.Caught {
					found := "Caught"
					if b.Violated != "" {
						found += ": breaks " + b.Violated
					}
					if b.Steps > 0 {
						found += " in " + counted(int64(b.Steps), "step")
					}
					return found, false
				}
				return "Not caught", true
			}
		}
	}
	return "", false
}

// projectView is a merged project's page: its links point at the receipt's
// commit, so they show what was checked.
func projectView(e readerEvidence) evidenceView {
	v := evidenceView{Kind: "evidence", Back: dashboardView(e.Repo), RepoShort: path.Base(e.Repo),
		Eyebrow: e.Repo + " · " + e.Dir, Title: upperFirst(e.Manifest.Name)}
	v.Chips = append(v.Chips, languageChip(e.Manifest.Language))
	if r := e.Report; r != nil {
		p := projectOf(projectSource{}, r)
		v.Chips = append(v.Chips, assuranceChip(p.Assurance))
		if r.Passed {
			v.Chips = append(v.Chips, evidenceChip{"Passed", "holds"})
		} else {
			v.Chips = append(v.Chips, evidenceChip{"Failed", "bug"})
		}
		if r.Design.DistinctStates > 0 {
			v.Chips = append(v.Chips, evidenceChip{counted(r.Design.DistinctStates, "state") + " checked", ""})
		}
	}
	v.Links = projectLinks(e)
	v.Groups = rulesOf(e.Lock.Statements, e.Report)
	v.Bounds = boundsOf(e.Lock.Bounds)
	return v
}

func projectLinks(e readerEvidence) []evidenceLink {
	var links []evidenceLink
	if rt := e.Lock.Ratified; rt != nil && rt.Issue > 0 {
		links = append(links, evidenceLink{"issue", fmt.Sprintf("Issue #%d", rt.Issue), "what was asked", onGitHub(e.Repo, "issues", fmt.Sprint(rt.Issue))})
		if by := strings.TrimPrefix(rt.By, "@"); by != "" {
			href := ""
			if strings.HasPrefix(rt.Comment, "https://github.com/"+e.Repo+"/issues/") || strings.HasPrefix(rt.Comment, "https://github.com/"+e.Repo+"/pull/") {
				href = rt.Comment
			}
			links = append(links, evidenceLink{"check", "Ratified by @" + by, dateOf(rt.At), href})
		}
	} else if decisionName.MatchString(e.Lock.Decision) {
		detail := "ratified by hand"
		if j := e.Report; j != nil && j.Journal != nil && j.Journal.By != "" {
			detail = "ratified by " + j.Journal.By
			if d := dateOf(j.Journal.At); d != "" {
				detail += ", " + d
			}
		}
		links = append(links, evidenceLink{"check", "Decision " + e.Lock.Decision, detail, githubAt(e.Repo, "blob", e.Commit, "decisions/journal/"+e.Lock.Decision+".jsonl")})
	}
	if e.Manifest.Module != "" {
		links = append(links, evidenceLink{"file", "The model", path.Base(e.Manifest.Module), githubAt(e.Repo, "blob", e.Commit, path.Join(e.Dir, e.Manifest.Module))})
	}
	for _, c := range existingOnGitHub(e) {
		links = append(links, evidenceLink{"file", "Code checked", c.label, githubAt(e.Repo, "blob", e.Commit, c.path)})
	}
	links = append(links, evidenceLink{"folder", "The project", e.Dir, githubAt(e.Repo, "tree", e.Commit, e.Dir)})
	if githubCommit.MatchString(e.Commit) {
		links = append(links, evidenceLink{"commit", "Receipt commit", e.Commit[:7], onGitHub(e.Repo, "commit", e.Commit)})
	}
	links = append(links, evidenceLink{"repo", e.Repo, "on GitHub", onGitHub(e.Repo)})
	return links
}

type codeLink struct{ label, path string }

// existingOnGitHub names the code a project checks without holding it, with
// its place in the repository where the receipt commit's tree showed it.
func existingOnGitHub(e readerEvidence) []codeLink {
	out := make([]codeLink, 0, len(e.Manifest.Existing))
	for _, x := range e.Manifest.Existing {
		out = append(out, codeLink{label: x, path: e.Code[x]})
	}
	return out
}

// existingCode places the code a project checks without holding it, as the
// gate finds it: TypeScript's in its package, the nearest folder above the
// project with a package-lock.json, and Python's in its application, the one
// folder above the project that holds every named source. A path the
// commit's tree doesn't hold isn't placed.
func existingCode(tree []github.TreeEntry, dir string, m project.Manifest) map[string]string {
	if len(m.Existing) == 0 || dir == "." {
		return nil
	}
	has := make(map[string]bool, len(tree))
	for _, t := range tree {
		has[t.Path] = true
	}
	join := func(root, p string) string {
		if root == "." {
			return p
		}
		return root + "/" + p
	}
	var above []string
	for d := path.Dir(dir); ; d = path.Dir(d) {
		above = append(above, d)
		if d == "." || d == "/" {
			break
		}
	}
	root := ""
	switch m.Language {
	case "typescript":
		for _, a := range above {
			if has[join(a, "package-lock.json")] {
				root = a
				break
			}
		}
	case "python":
		found := 0
		for _, a := range above {
			all := true
			for _, x := range m.Existing {
				all = all && has[join(a, x)]
			}
			if all {
				root, found = a, found+1
			}
		}
		if found != 1 {
			root = ""
		}
	}
	if root == "" {
		return nil
	}
	out := map[string]string{}
	for _, x := range m.Existing {
		if p := join(root, x); has[p] {
			out[x] = p
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dateOf(at string) string {
	if len(at) >= 10 {
		return at[:10]
	}
	return at
}

// proposalView is a proposal's page, before anyone ratifies it.
func proposalView(e comparisonEvidence) evidenceView {
	v := evidenceView{Kind: "proposal", Back: dashboardView(e.Repo), RepoShort: path.Base(e.Repo),
		Eyebrow: fmt.Sprintf("%s · issue #%d", e.Repo, e.Issue)}
	v.Chips = append(v.Chips, evidenceChip{"Awaiting ratification", "people"})
	if p := e.Proposal; p != nil {
		v.Title = upperFirst(p.Name)
		if p.Language != "" {
			v.Chips = append([]evidenceChip{languageChip(p.Language)}, v.Chips...)
		}
		v.Chips = append(v.Chips, evidenceChip{counted(int64(len(p.Statements)), "rule"), ""})
		v.Groups = rulesOf(p.Statements, nil)
		v.Bounds = boundsOf(p.Bounds)
	}
	v.Links = issueLinks(e.Repo, e.Issue)
	return v
}

// previewView is an unratified alternative's page.
func previewView(e previewEvidence) evidenceView {
	v := evidenceView{Kind: "alternative", Back: dashboardView(e.Repo), RepoShort: path.Base(e.Repo),
		Eyebrow: fmt.Sprintf("%s · issue #%d · %s/%s", e.Repo, e.Issue, e.Fork, e.Option.ID),
		Title:   fmt.Sprintf("Alternative %s/%s", e.Fork, e.Option.ID)}
	v.Chips = append(v.Chips, evidenceChip{"Unratified alternative", "people"}, evidenceChip{"Model only", ""})
	if pv := e.Option.Preview; pv != nil && pv.Model != nil {
		v.Groups = rulesOf(pv.Model.Statements, pv.Receipt)
		v.Bounds = boundsOf(pv.Model.Bounds)
	}
	v.Links = issueLinks(e.Repo, e.Issue)
	return v
}

func issueLinks(repo string, issue int) []evidenceLink {
	var links []evidenceLink
	if issue > 0 {
		links = append(links, evidenceLink{"issue", fmt.Sprintf("Issue #%d", issue), "the request and the factory's comments", onGitHub(repo, "issues", fmt.Sprint(issue))})
	}
	return append(links, evidenceLink{"repo", repo, "on GitHub", onGitHub(repo)})
}
