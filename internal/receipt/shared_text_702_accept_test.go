package receipt_test

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestSharedTextEscapingPreservesReceiptCompatibility(t *testing.T) {
	// This exported API is also the factory's text encoder. It returns text,
	// not a code wrapper: GitHub mentions need a separate code context.
	encoded := receipt.EscapeText("@user @org/team #123 owner/repository#123")
	if got := explicit247Text(t, encoded); got != "@user @org/team #123 owner/repository#123" {
		t.Fatalf("shared encoder changed reference text: %q", got)
	}
	for _, value := range []string{
		"", "go:example.test/core#Step@core/step.go", "plain 'single' \"double\" café 界",
		"&amp; &#95; &#x40; &quot; &lt;code&gt; & raw",
		"` `` ``` ```` \\` \\| \\\\| | **bold** _emphasis_ ~~strike~~ [link](https://evil.invalid)",
		"</code><img src=x onerror='alert(1)'><script>bad</script><!-- comment -->",
		"http://evil.invalid HTTPS://evil.invalid FTP://evil.invalid www.evil.invalid a+b@evil.invalid",
		"first\rsecond\nthird\r\n| forged | fourth",
	} {
		got := receipt.EscapeText(value)
		if visible := explicit247Text(t, got); visible != value {
			t.Errorf("encoder displays %q, want %q", visible, value)
		}
		// Reuse the independent receipt oracle: GFM splits table cells
		// before parsing spans, and inline HTML does not disable autolinks.
		cells := componentMDCells("| " + got + " | sentinel |")
		if len(cells) != 2 || cells[1] != "sentinel" {
			t.Errorf("text changed table boundaries: %#v", cells)
		}
	}
	if got := receipt.EscapeText("go:example.test/core#Step@core/step.go"); got != "go:example.test/core#Step@core/step.go" {
		t.Errorf("portable component identity lost its current spelling: %q", got)
	}

	value := "No_Double &amp; &#95; `tick` | @user @org/team #123 owner/repository#123 \"q\" 'q' </code><img src=x> https://evil.invalid www.evil.invalid a@evil.invalid\r\nend"
	r := explicit247Report(t, "core/step.go", false)
	r.Project, r.GeneratedAt = value, value
	md := explicit247RenderUnchanged(t, &r) // Also checks JSON and fingerprints.
	heading := strings.TrimPrefix(explicit247Line(t, md, "## ◉ Invariant receipt · "), "## ◉ Invariant receipt · ")
	if heading != receipt.EscapeText(value) || explicit247Visible(t, heading) != value {
		t.Errorf("receipt and exported encoder disagree: %q", heading)
	}
	footer := explicit247Line(t, md, "<sub>Generated ")
	if !strings.Contains(footer, receipt.EscapeText(value)+" from recorded checks, supplied data and declared assumptions.") {
		t.Errorf("component receipt lost literal timestamp or provenance: %q", footer)
	}

	// The same pre-existing golden bytes used by the receipt compatibility
	// tests: exporting the encoder must not change legacy receipt output.
	goldens := []struct{ fingerprint, markdown string }{
		{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "fa2c20aa584f9ba051a6e8f89b97843086750f307ac6e09d0c1b646273381e15"},
		{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "dd24cf0587a3a16d7f372d38f562402c2f3a6738b4587ee064184dd192d91728"},
		{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "040b3e9231b957ca2bd8e2ff20585fde0798b6b392d6ab9e750b51c5908e447d"},
	}
	for i, legacy := range receiptAssuranceFixtures() {
		legacy.Fingerprint = verify.Fingerprint(legacy)
		text := explicit247RenderUnchanged(t, &legacy)
		if legacy.Fingerprint != goldens[i].fingerprint || fmt.Sprintf("%x", sha256.Sum256([]byte(readerLegacyBody(t, text)))) != goldens[i].markdown {
			t.Errorf("legacy %s changed its fingerprint or Markdown", legacy.Project)
		}
	}
}
