package formalize_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/formalize"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestComparison717ReferenceReloadAndInlineAgreement(t *testing.T) {
	_, p, _ := comparison717Checked(t)
	for _, mode := range []string{"both agree", "null", "array", "string", "fractional version", "null fingerprint", "absolute path", "backslash", "directory", "inline differs", "inline damaged", "missing after success", "ordinary after success", "ordinary with sibling", "legacy after success"} {
		t.Run(mode, func(t *testing.T) {
			dir := comparison717WriteProject(t, p)
			c, err := comparison717Current(t, dir, p)
			if err != nil || c.Reader == nil {
				t.Fatalf("initial read: %v", err)
			}
			wire := comparison717Reference(t, p)
			ref := wire["comparison_ref"].(map[string]any)
			sibling := filepath.Join(dir, ".invariant/comparison.json")
			switch mode {
			case "both agree":
				wire["comparison"] = p.Reader.Comparison
			case "null":
				wire["comparison_ref"] = nil
			case "array":
				wire["comparison_ref"] = []any{ref}
			case "string":
				wire["comparison_ref"] = "comparison.json"
			case "fractional version":
				ref["version"] = 1.5
			case "null fingerprint":
				ref["fingerprint"] = nil
			case "absolute path":
				ref["file"] = sibling
			case "backslash":
				ref["file"] = `..\comparison.json`
			case "directory":
				if err := os.Remove(sibling); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(sibling, 0700); err != nil {
					t.Fatal(err)
				}
			case "inline differs":
				other, err := statechart.Compare(p.Reader.Comparison.Baseline, p.Reader.Comparison.Baseline)
				if err != nil {
					t.Fatal(err)
				}
				wire["comparison"] = other
			case "inline damaged":
				other := reader603Object(t, p.Reader.Comparison)
				other["fingerprint"] = "damaged"
				wire["comparison"] = other
			case "ordinary with sibling":
				delete(wire, "comparison_ref")
			case "missing after success", "ordinary after success", "legacy after success":
				if err := os.Remove(sibling); err != nil {
					t.Fatal(err)
				}
				if mode != "missing after success" {
					delete(wire, "comparison_ref")
				}
			}
			readerFile := filepath.Join(dir, ".invariant/reader.json")
			reader603Write(t, readerFile, string(reader603JSON(t, wire)))
			if mode == "legacy after success" {
				if err := os.Remove(readerFile); err != nil {
					t.Fatal(err)
				}
			}
			err = c.ReadReader(dir)
			switch mode {
			case "both agree":
				if err != nil || !bytes.Equal(reader603JSON(t, c.Reader), reader603JSON(t, p.Reader)) {
					t.Fatalf("agreed evidence changed on reload: %v", err)
				}
			case "ordinary after success", "ordinary with sibling":
				if err != nil || c.Reader == nil || c.Reader.Comparison != nil {
					t.Fatalf("ordinary reload retained a comparison: %v", err)
				}
			case "legacy after success":
				if err != nil || c.Reader != nil {
					t.Fatalf("legacy reload retained a reader: %v", err)
				}
			default:
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "comparison") || c.Reader != nil {
					t.Fatalf("failed read retained or substituted evidence: %v", err)
				}
			}
		})
	}
	// Project serialization is local to Write; plain Reader JSON remains an
	// independent, complete artifact even after a reference has been loaded.
	dir := comparison717WriteProject(t, p)
	c, err := comparison717Current(t, dir, p)
	if err != nil {
		t.Fatal(err)
	}
	var saved formalize.Reader
	if err := json.Unmarshal(reader603JSON(t, c.Reader), &saved); err != nil || saved.Comparison == nil {
		t.Fatalf("loaded reader ceased to be self-contained: %v", err)
	}
}
