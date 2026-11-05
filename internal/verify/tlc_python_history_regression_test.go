package verify_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/tlc"
	"github.com/gitdek/invariant/internal/verify"
)

func TestPythonApplicationTLCMemoryHistory(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			tc, _ := pa327Fake(t)
			bin, err := exec.LookPath("docker")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(filepath.Dir(bin), "docker.py")
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			script := strings.Replace(string(raw), "print('pa327 deliberately unavailable Docker'); sys.exit(73)", "print(json.dumps({'NCPU':8,'MemTotal':17179869184})); sys.exit(0)", 1)
			script = strings.Replace(script, "    msg(2262,'TLC2 Version", `    count=0
    if module=='Invariant_Conformance':
        pairs=body.split('Invariant_Steps == {',1)[1].split('}',1)[0]
        count=pairs.count('<<')
    if os.environ.get('PA540_MEMORY')=='1' and (module=='Lifecycle' or count>1) and args[args.index('--memory')+1]=='2048m':
        print('java.lang.OutOfMemoryError: Java heap space'); sys.exit(1)
    msg(2262,'TLC2 Version`, 1)
			if err := os.WriteFile(file, []byte(script), 0600); err != nil {
				t.Fatal(err)
			}
			f := pa327New(t, explicit, false)
			t.Setenv("PA540_MEMORY", "")
			baseline := pa327Run(t, f, tc)
			pa327Assert(t, f, baseline, true)
			t.Setenv("PA540_MEMORY", "1")
			r := pa327Run(t, f, tc)
			pa327Assert(t, f, r, true)
			if r.Fingerprint != baseline.Fingerprint || !reflect.DeepEqual(r.Design, baseline.Design) {
				t.Fatal("resource recovery changed application evidence")
			}
			attempts, wire := tlc540Attempts(t, r)
			groups := map[string][]tlc540Attempt{}
			for _, a := range attempts {
				groups[a.Check] = append(groups[a.Check], a)
			}
			seen := map[string]bool{}
			for _, group := range groups {
				module := group[0].Module
				if module != "Lifecycle" && module != "Invariant_Conformance" {
					if len(group) != 1 {
						t.Fatalf("unrelated check retried: %+v", group)
					}
					continue
				}
				seen[module] = true
				if module == "Invariant_Conformance" {
					if len(group) != 1 || group[0].MemoryBytes != 2<<30 || group[0].HeapBytes != 1536<<20 || group[0].Outcome != tlc.Passed && group[0].Outcome != tlc.Failed {
						t.Fatalf("application conformance did not split at ordinary limits: %+v", group)
					}
					continue
				}
				if len(group) != 2 || group[0].Outcome != tlc.Failed || group[1].Outcome != tlc.Passed ||
					group[0].MemoryBytes != 2<<30 || group[1].MemoryBytes != 12<<30 || group[1].HeapBytes != 9<<30 {
					t.Fatalf("application lost model memory retry: %+v", group)
				}
			}
			if !seen["Lifecycle"] || !seen["Invariant_Conformance"] {
				t.Fatal("application omitted a phase's resource history")
			}
			splits := 0
			for _, a := range r.TLCAttempts {
				if a.Split {
					splits++
					if a.Module != "Invariant_Conformance" || a.Range == nil || a.Range.Count != 2 || a.MemoryBytes != 2<<30 || !strings.Contains(a.Message, "memory") {
						t.Fatalf("invalid conformance split history: %+v", a)
					}
				}
			}
			if splits != len(r.Application.Conformance) {
				t.Fatal("application omitted exhausted conformance parents")
			}
			var decoded verify.Report
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if verify.Fingerprint(decoded) != baseline.Fingerprint {
				t.Fatal("decoded application fingerprint includes resource history")
			}
			for _, text := range []string{receipt.Markdown(&decoded), verify.Feedback(&decoded)} {
				for _, want := range []string{"Lifecycle", "2048", "1536", "12288", "9216", "MiB", "memory", "retry"} {
					if !strings.Contains(text, want) {
						t.Errorf("application resource history omitted %q: %s", want, text)
					}
				}
			}
		})
	}
}
