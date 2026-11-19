package verify_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

func TestReader944ReceiptReplayAndDiagnostics(t *testing.T) {
	tc, log := reader615Tools(t)
	dir := reader615Project(t, 500)
	r := reader615Run(t, dir, tc, true)
	if !r.Passed || r.Reader == nil || r.Reader.Chart == nil || r.Reader.Limitation != nil || r.Reader.Unavailable != nil {
		t.Fatal("complete 501-state receipt did not draw")
	}
	if r.Reader.Chart.Report.Counts["tlc_states"] != 501 || r.Reader.Chart.Report.Counts["tlc_steps"] != 1002 {
		t.Fatal("receipt sampled complete graph")
	}
	replay := false
	for _, call := range reader615Calls(t, log) {
		if call["dump"] != "" && strings.HasPrefix(call["module"], "InvariantReader") {
			replay = true
		}
	}
	if !replay {
		t.Fatal("receipt did not replay locked invariants")
	}
	before := r.Fingerprint
	reader615Write(t, filepath.Join(dir, ".invariant/view.json"), strings.ReplaceAll(reader615View, `"var":"n"`, `"var":"absent"`))
	bad := reader615Run(t, dir, tc, true)
	if !bad.Passed || bad.Fingerprint != before || bad.Reader.Chart != nil || bad.Reader.Limitation != nil || bad.Reader.Unavailable == nil || bad.Reader.Unavailable.Code != "evaluation" {
		t.Fatal("runtime view failure changed verdict/verification identity or became size success")
	}
	reader615Write(t, filepath.Join(dir, ".invariant/view.json"), reader615View)
	r = reader615Run(t, dir, tc, true)
	reader944Archive(t, r)
}

func reader944Archive(t *testing.T, r *verify.Report) {
	t.Helper()
	dir := t.TempDir()
	before, _ := json.Marshal(r)
	if err := receipt.Write(dir, r); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Fatal("unexpected nested archive evidence")
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 60<<20 {
			t.Fatalf("%s lacks 4 MiB archive headroom: %d bytes", e.Name(), len(data))
		}
		w, err := zw.Create("projection/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.UncompressedSize64 > 64<<20 {
			t.Fatal("archive reader would refuse", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		if strings.HasSuffix(f.Name, "/receipt.json") {
			var saved verify.Report
			if err := json.Unmarshal(data, &saved); err != nil || saved.Fingerprint != r.Fingerprint || saved.Passed != r.Passed {
				t.Fatal("archive changed verification outcome", err)
			}
		}
	}
	after, _ := json.Marshal(r)
	if !bytes.Equal(before, after) {
		t.Fatal("receipt writing mutated or resealed evidence")
	}
}
