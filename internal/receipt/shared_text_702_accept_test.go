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
		{"sha256:12362a3a9c164fea85ae6ea6ff4bf623d16ed3136adfbbdb211ea8161007453a", "5369ee9782157a6c61127a8f3b245b968c5e7ec7784f43a95b914275a2fa33e8"},
		{"sha256:8d0e0ac4ecf82433ffb6f0971b45a14bafc96fa10407766f3bcae0b76dc50fda", "7fa37f965421a4d438b8d9a10c61a9d995fba8b4330ea043953853b62faa0a6a"},
		{"sha256:295f04994adf44460b659ba6ded42792ceab506707fae3516ef90e7fab2d475e", "f2c9dac64f821b06af013e560296fde59d2c9f9f78ab5626c830e6305ff6b458"},
	}
	for i, legacy := range receiptAssuranceFixtures() {
		legacy.Fingerprint = verify.Fingerprint(legacy)
		text := explicit247RenderUnchanged(t, &legacy)
		if legacy.Fingerprint != goldens[i].fingerprint || fmt.Sprintf("%x", sha256.Sum256([]byte(readerLegacyBody(t, text)))) != goldens[i].markdown {
			t.Errorf("legacy %s changed its fingerprint or Markdown", legacy.Project)
		}
	}
}
