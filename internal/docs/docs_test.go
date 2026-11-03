package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// readmeBudget is how long the README may grow. It's the front page, so
// detail goes in the page under docs/ it links (D-0294).
const readmeBudget = 400

// root is the repository's root, two directories up from this package.
const root = "../.."

func TestTheReadmeStaysAFrontPage(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n > readmeBudget {
		t.Errorf("README.md is %d lines, past its budget of %d: move the detail to the page under docs/ it belongs in, and link it", n, readmeBudget)
	}
}

// The README and every page under docs/ link only to files that exist, and
// to headings those files have, so moving a section can't strand a link.
func TestTheDocsLinksLeadSomewhere(t *testing.T) {
	pages := []string{"README.md"}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			pages = append(pages, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]map[string]bool{}
	anchorsOf := func(page string) map[string]bool {
		if a, ok := anchors[page]; ok {
			return a
		}
		b, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatal(err)
		}
		anchors[page] = headingAnchors(string(b))
		return anchors[page]
	}
	for _, page := range pages {
		b, err := os.ReadFile(filepath.Join(root, page))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range links(string(b)) {
			target, anchor, _ := strings.Cut(l.target, "#")
			resolved := page
			if target != "" {
				resolved = filepath.Join(filepath.Dir(page), target)
				if _, err := os.Stat(filepath.Join(root, resolved)); err != nil {
					t.Errorf("%s:%d links to %s, which doesn't exist", page, l.line, l.target)
					continue
				}
			}
			if anchor != "" && strings.HasSuffix(resolved, ".md") && !anchorsOf(resolved)[anchor] {
				t.Errorf("%s:%d links to %s, which has no heading #%s", page, l.line, filepath.ToSlash(resolved), anchor)
			}
		}
	}
}

type link struct {
	line   int
	target string
}

var (
	mdLink   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	htmlLink = regexp.MustCompile(`(?:src|srcset|href)="([^"]+)"`)
	codeSpan = regexp.MustCompile("`[^`]*`")
	external = regexp.MustCompile(`^(?:[a-z]+:|//)`)
)

// links lists a page's relative links, outside code.
func links(text string) []link {
	var out []link
	fence := false
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		line = codeSpan.ReplaceAllString(line, "")
		for _, re := range []*regexp.Regexp{mdLink, htmlLink} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				if !external.MatchString(m[1]) {
					out = append(out, link{line: i + 1, target: m[1]})
				}
			}
		}
	}
	return out
}

var (
	heading  = regexp.MustCompile(`^#{1,6} (.+?)\s*#*\s*$`)
	inline   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	explicit = regexp.MustCompile(`<a (?:id|name)="([^"]+)"`)
)

// headingAnchors lists the anchors GitHub gives a page's headings: the
// heading's text, lowercased, with only letters, digits, spaces, hyphens and
// underscores kept and each space a hyphen, numbered from -1 on a repeat.
func headingAnchors(text string) map[string]bool {
	out := map[string]bool{}
	seen := map[string]int{}
	fence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		for _, m := range explicit.FindAllStringSubmatch(line, -1) {
			out[m[1]] = true
		}
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		words := inline.ReplaceAllString(m[1], "$1")
		var slug strings.Builder
		for _, r := range strings.ToLower(words) {
			switch {
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
				slug.WriteRune(r)
			case r == ' ':
				slug.WriteRune('-')
			}
		}
		s := slug.String()
		if n := seen[s]; n > 0 {
			out[s+"-"+strconv.Itoa(n)] = true
		} else {
			out[s] = true
		}
		seen[s]++
	}
	return out
}
