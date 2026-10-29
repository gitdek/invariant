package dashboard

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The public stylesheet gives every shell kind the same color in its
// tool tick, duration band and legend, and a color distinct from the other
// shell kinds in each theme. This checks the served kind-class contract;
// it does not require a browser or claim to exercise the page's script.
func TestShellKindsHaveDistinctStripColors(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/style.css", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /style.css: status %d, want 200", w.Code)
	}

	// Read the stylesheet's flat rules, including each selector in a
	// grouped rule. Strip comments first so a commented-out rule cannot
	// supply a missing color. The page defines theme colors on :root and
	// overrides them on :root[data-theme="light"].
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(w.Body.String(), "")
	rules := map[string]map[string]string{}
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, selector := range strings.Split(rule[1], ",") {
			selector = strings.Join(strings.Fields(selector), " ")
			if rules[selector] == nil {
				rules[selector] = map[string]string{}
			}
			for _, declaration := range strings.Split(rule[2], ";") {
				pair := strings.SplitN(declaration, ":", 2)
				if len(pair) == 2 {
					rules[selector][strings.TrimSpace(pair[0])] = strings.TrimSpace(pair[1])
				}
			}
		}
	}

	kinds := []string{"read", "search", "list", "build", "test", "git", "other"}
	families := []struct{ before, after, property string }{
		{".strip .tool.", "", "stroke"},
		{".strip .span.", "", "fill"},
		{".legend .", " i", "background"},
	}
	variable := regexp.MustCompile(`var\(\s*(--[\w-]+)\s*\)`)
	for _, theme := range []string{"dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			vars := map[string]string{}
			for name, value := range rules[":root"] {
				if strings.HasPrefix(name, "--") {
					vars[name] = value
				}
			}
			if theme == "light" {
				for _, selector := range []string{`:root[data-theme="light"]`, ":root[data-theme='light']", ":root[data-theme=light]"} {
					for name, value := range rules[selector] {
						if strings.HasPrefix(name, "--") {
							vars[name] = value
						}
					}
				}
			}
			resolve := func(value string) string {
				// Resolve aliases too, with a bound so a variable cycle
				// fails instead of hanging the acceptance check.
				missing := false
				for i := 0; i <= len(vars) && variable.MatchString(value); i++ {
					value = variable.ReplaceAllStringFunc(value, func(reference string) string {
						resolved, ok := vars[variable.FindStringSubmatch(reference)[1]]
						missing = missing || !ok
						return resolved
					})
				}
				value = strings.ToLower(strings.Join(strings.Fields(value), ""))
				if missing || strings.Contains(value, "var(") {
					return ""
				}
				value = strings.TrimSuffix(value, "!important")
				// The stylesheet uses hex colors; #abc and #aabbcc
				// must not count as different colors.
				if len(value) == 4 && value[0] == '#' {
					value = "#" + strings.Repeat(value[1:2], 2) + strings.Repeat(value[2:3], 2) + strings.Repeat(value[3:4], 2)
				}
				return value
			}

			used := map[string]string{}
			for _, kind := range kinds {
				var color string
				for _, family := range families {
					selector := family.before + kind + family.after
					value := rules[selector][family.property]
					got := resolve(value)
					if got == "" || got == "none" || got == "transparent" || got == "inherit" || got == "initial" || got == "currentcolor" {
						t.Errorf("%s has no explicit %s color (got %q)", selector, family.property, value)
						continue
					}
					if color == "" {
						color = got
					} else if got != color {
						t.Errorf("%s %s resolves to %q; %s uses %q elsewhere in its strip and legend", selector, family.property, got, kind, color)
					}
				}
				if color == "" {
					continue
				}
				if other, ok := used[color]; ok {
					t.Errorf("%s and %s share color %s; each shell kind needs its own color", kind, other, color)
				}
				used[color] = kind
			}
		})
	}
}
