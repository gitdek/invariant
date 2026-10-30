package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// These are the served page's accessibility hooks, not Go implementation
// helpers: one page-local checkbox and one output area per live card.
// The script must use the checkbox to opt in to /act/api/live.json?commands=1,
// render each command as text with its kind and times, and clear the areas
// on opt-out, step replacement/end or a failed private read. The checkbox
// is not persisted; an old in-flight read must not undo turning it off.
// This test checks the initial HTTP/HTML contract without a browser runtime.
func TestActShellCommandControlStartsOff(t *testing.T) {
	f := newAct236Fixture(t, "codex")
	f.append(t, f.start(t, "page", "printf 'PRIVATE_PAGE_236'", 1))
	h := f.s.Handler()
	for _, path := range []string{"/act", "/act?commands=1", "/act?issue=owner/app%23236"} {
		w := act236Request(h, "GET", path, f.owner)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("%s: HTTP %d, headers %v", path, w.Code, w.Header())
		}
		page := w.Body.String()
		act236NoText(t, w, "PRIVATE_PAGE_236")
		var controls []string
		for _, input := range regexp.MustCompile(`(?is)<input\b[^>]*>`).FindAllString(page, -1) {
			if act236Attr(input, "id") == "show-shell-commands" {
				controls = append(controls, input)
			}
		}
		if len(controls) != 1 {
			t.Fatalf("%s has %d shell command controls; want one input with id=show-shell-commands", path, len(controls))
		}
		control := controls[0]
		if act236Attr(control, "type") != "checkbox" || act236HasAttr(control, "checked") || act236HasAttr(control, "disabled") {
			t.Errorf("%s control must be an enabled, unchecked checkbox: %s", path, control)
		}
		if act236Attr(control, "aria-controls") != "live-cards" {
			t.Errorf("checkbox does not identify the live cards it controls: %s", control)
		}
		labelled := false
		for _, label := range regexp.MustCompile(`(?is)<label\b([^>]*)>(.*?)</label>`).FindAllStringSubmatch(page, -1) {
			if act236Attr("<label "+label[1]+">", "for") == "show-shell-commands" && strings.Contains(label[2], "Show shell commands") {
				labelled = true
			}
		}
		if !labelled {
			t.Error("the command checkbox has no associated Show shell commands label")
		}
		live := regexp.MustCompile(`(?is)<section\b[^>]*\bid="live"[^>]*>(.*?)</section>`).FindStringSubmatch(page)
		if len(live) != 2 || !strings.Contains(live[1], control) {
			t.Fatal("command toggle is not in the live section beside the cards")
		}
		card := regexp.MustCompile(`(?is)<template\b[^>]*\bid="live-card"[^>]*>(.*?)</template>`).FindStringSubmatch(live[1])
		if len(card) != 2 {
			t.Fatal("private page lost the existing live-card template")
		}
		var strip, commands string
		for _, tag := range regexp.MustCompile(`(?is)<[a-z][a-z0-9]*\b[^>]*>`).FindAllString(card[1], -1) {
			classes := " " + act236Attr(tag, "class") + " "
			if strings.Contains(classes, " strip ") {
				strip = tag
			}
			if strings.Contains(classes, " shell-commands ") {
				commands = tag
			}
		}
		if strip == "" || commands == "" || !act236HasAttr(commands, "hidden") {
			t.Errorf("each private live card needs its unchanged strip and an initially hidden shell-commands area: strip %q, commands %q", strip, commands)
		}
	}
	for _, path := range []string{"/", "/index.html", "/?commands=1"} {
		w := act236Request(h, "GET", path, f.owner)
		if w.Code != 200 {
			t.Fatalf("public page %s: HTTP %d", path, w.Code)
		}
		act236NoText(t, w, "PRIVATE_PAGE_236", "show-shell-commands", "Show shell commands")
	}
}

func act236Attr(tag, name string) string {
	pattern := `(?i)\s` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`
	m := regexp.MustCompile(pattern).FindStringSubmatch(tag)
	if len(m) == 0 {
		return ""
	}
	return m[1] + m[2] + m[3]
}

func act236HasAttr(tag, name string) bool {
	return regexp.MustCompile(`(?i)\s` + regexp.QuoteMeta(name) + `(?:\s|=|>)`).MatchString(tag)
}
