package plumbing

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Exercise the embedded signer's byte budget without installed frameworks.
// The real precheck and both framework examples use the offline image fixtures.
func TestNodeDiagnosticFramesRetainEscapedUnicode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("local Node is unavailable; pinned-image diagnostic coverage is opt-in")
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			root := t.TempDir()
			in, _, out := nodeEvidenceFixture(t, framework, "assertion")
			in.Binding.Selector = strings.Repeat("selected title ", 90)
			in.Source = "test source\n"
			in.Binding.SourceDigest = dependencyHash([]byte(in.Source))
			for name, data := range map[string][]byte{
				"package.json":           []byte(`{}`),
				"ready.test.ts":          []byte(in.Source),
				"node_runner_common.cjs": nodeRunnerCommon,
				"key":                    nodeEvidenceKey,
			} {
				if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			key, err := os.Open(filepath.Join(root, "key"))
			if err != nil {
				t.Fatal(err)
			}
			defer key.Close()
			diagnostics := "ready.test.ts:1:1 TS2322: " + strings.Repeat("quoted \"\\\t 🔎 漢\x00\r\n", 1500) + "END_OF_DIAGNOSTICS\n"
			input, err := json.Marshal(struct {
				Binding     RepositoryAcceptanceBinding
				File        string
				Diagnostics string
			}{in.Binding, "ready.test.ts", diagnostics})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, node, "-e", `const common = require('./node_runner_common.cjs');
const input = JSON.parse(process.argv[1]);
const session = common.open(input, 'parent', 'events.jsonl');
session.emit('begin');
session.diagnostic(input.Diagnostics);
session.emit('refused', {Reason: 'precheck diagnostics'});
session.close();`, string(input))
			cmd.Dir = root
			cmd.ExtraFiles = []*os.File{key, key} // The parent consumes only fd 4.
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("diagnostic writer: %v %s", err, output)
			}
			out.Events, err = os.ReadFile(filepath.Join(root, "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			lines := bytes.Split(bytes.TrimSuffix(out.Events, []byte("\n")), []byte("\n"))
			if len(lines) < 12 {
				t.Fatal("fixture did not cross a sequence-number width boundary")
			}
			for _, line := range lines {
				if len(line)+1 > 4096 || !utf8.Valid(line) {
					t.Fatal("diagnostic frame exceeded atomic bytes or split UTF-8")
				}
			}
			evidence, err := collectNodeAcceptance(in, out, nodeEvidenceKey)
			want := "Node execution refused: precheck diagnostics\n" + diagnostics
			if err == nil || err.Error() != want || evidence.Complete || len(evidence.Records) != 1 || evidence.Records[0].Reason != want {
				t.Fatalf("signed chunks lost refusal detail: %v %+v", err, evidence)
			}
			out.Events = bytes.Replace(out.Events, []byte("TS2322"), []byte("TS2307"), 1)
			evidence, err = collectNodeAcceptance(in, out, nodeEvidenceKey)
			if err == nil || evidence.Complete || !strings.Contains(err.Error(), "authentication failed") || strings.Contains(err.Error(), "TS2307") {
				t.Fatalf("altered diagnostic was trusted: %v %+v", err, evidence)
			}
		})
	}
}
