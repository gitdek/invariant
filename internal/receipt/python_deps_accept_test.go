package receipt_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/receipt"
	"github.com/gitdek/invariant/internal/verify"
)

// The JSON boundary keeps these acceptance tests independent of the concrete
// Go fields used to carry application dependency evidence into a receipt.
func pythonDependenciesAcceptDocument() map[string]any {
	return map[string]any{
		"project":   "python application dependencies",
		"passed":    true,
		"assurance": "tested against the model",
		"bounds":    map[string]any{"Capacity": "2"},
		"build":     map[string]any{"passed": true},
		"toolchain": map[string]any{
			"python_image":  "python@sha256:" + strings.Repeat("ab", 32),
			"nagini_recipe": "sha256:" + strings.Repeat("cd", 32),
			"python_dependencies": map[string]any{
				"base":     "python@sha256:" + strings.Repeat("ef", 32),
				"recipe":   "sha256:" + strings.Repeat("12", 32),
				"platform": "linux/amd64",
				"inputs": []any{
					map[string]any{"path": "requirements.txt", "sha256": "sha256:" + strings.Repeat("34", 32)},
					map[string]any{"path": "requirements/constraints.txt", "sha256": "sha256:" + strings.Repeat("56", 32)},
				},
				"canonical_inputs": []any{
					map[string]any{"path": "constraints.txt", "sha256": "sha256:" + strings.Repeat("a1", 32)},
					map[string]any{"path": "requirements.txt", "sha256": "sha256:" + strings.Repeat("b2", 32)},
				},
				"artifacts": []any{
					map[string]any{"name": "idna", "version": "3.10", "filename": "idna-3.10-py3-none-any.whl", "sha256": "sha256:" + strings.Repeat("78", 32)},
					map[string]any{"name": "six", "version": "1.16.0", "filename": "six-1.16.0-py2.py3-none-any.whl", "sha256": "sha256:" + strings.Repeat("9a", 32)},
				},
			},
		},
	}
}

func pythonDependenciesAcceptReport(t *testing.T, document map[string]any) verify.Report {
	t.Helper()
	b, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var r verify.Report
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func pythonDependenciesAcceptEvidence(document map[string]any) map[string]any {
	return document["toolchain"].(map[string]any)["python_dependencies"].(map[string]any)
}

func pythonDependenciesAcceptSetImage(t *testing.T, r *verify.Report, image string) {
	t.Helper()
	// Image is deliberately absent from JSON. Set the execution-only value
	// directly so the exclusion check exercises a selected runtime image.
	fields := reflect.ValueOf(&r.Toolchain).Elem()
	for i := 0; i < fields.NumField(); i++ {
		if strings.Split(fields.Type().Field(i).Tag.Get("json"), ",")[0] != "python_dependencies" {
			continue
		}
		evidence := fields.Field(i)
		if evidence.Kind() == reflect.Pointer && !evidence.IsNil() {
			evidence = evidence.Elem()
		}
		if evidence.Kind() == reflect.Struct {
			value := evidence.FieldByName("Image")
			if value.IsValid() && value.CanSet() && value.Kind() == reflect.String {
				value.SetString(image)
				return
			}
		}
	}
	t.Fatal("Python dependency evidence must retain the execution-only Image field")
}

// Hashes may use the receipt's existing twelve-hex-digit display convention.
func pythonDependenciesAcceptShort(hash string) string {
	if i := strings.Index(hash, "sha256:"); i >= 0 && len(hash) >= i+19 {
		return hash[:i+19]
	}
	return hash
}

func TestPythonReceiptIdentifiesResolvedDependencies(t *testing.T) {
	document := pythonDependenciesAcceptDocument()
	r := pythonDependenciesAcceptReport(t, document)
	pythonDependenciesAcceptMarkdown(t, document, r)
	pythonDependenciesAcceptMarkdown(t, document, pythonDependenciesAcceptAttributed(t, r))
}

// The existing attribution API can preserve shared environment evidence
// without enabling Python component discovery or execution.
func pythonDependenciesAcceptAttributed(t *testing.T, r verify.Report) verify.Report {
	t.Helper()
	sourceHash := "sha256:" + strings.Repeat("d7", 32)
	inv := &project.ComponentInventory{
		Mode:  "explicit",
		Scope: []string{"core"},
		Files: []project.ComponentFile{{Path: "core/ops.go", SHA256: sourceHash}},
		Components: []project.Component{{
			ID: "go:example.com/app/core#Step@core/ops.go", Language: "go",
			Package: "example.com/app/core", Symbol: "Step", Source: "core/ops.go", SourceSHA256: sourceHash,
		}},
	}
	if err := r.AttributeComponentProofs(inv, nil); err != nil {
		t.Fatal(err)
	}
	if r.CodeAssurance() != "component evidence" || len(r.Components.Proofs) != 1 || r.Components.Proofs[0].Status != "unchecked" {
		t.Fatalf("environment evidence must not establish component proof: %#v", r.Components)
	}
	return r
}

func pythonDependenciesAcceptMarkdown(t *testing.T, document map[string]any, r verify.Report) {
	t.Helper()
	md := receipt.Markdown(&r)
	evidence := pythonDependenciesAcceptEvidence(document)
	appRecipe := pythonDependenciesAcceptShort(evidence["recipe"].(string))
	naginiRecipe := pythonDependenciesAcceptShort(document["toolchain"].(map[string]any)["nagini_recipe"].(string))
	for _, want := range []string{appRecipe, pythonDependenciesAcceptShort(evidence["base"].(string)), evidence["platform"].(string), naginiRecipe} {
		if !strings.Contains(md, want) {
			t.Errorf("receipt omits dependency or verifier provenance %q:\n%s", want, md)
		}
	}
	// The application's recipe and Nagini's recipe must have distinct
	// descriptions, so an application package is not presented as prover input.
	var applicationNamed, naginiNamed bool
	for _, line := range strings.Split(md, "\n") {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "python") && strings.Contains(lower, "depend") {
			applicationNamed = true
		}
		if strings.Contains(line, naginiRecipe) && strings.Contains(lower, "nagini") && !strings.Contains(line, appRecipe) {
			naginiNamed = true
		}
	}
	if !applicationNamed || !naginiNamed {
		t.Errorf("receipt must distinguish Python application dependencies from the pinned Nagini verifier:\n%s", md)
	}
	for _, kind := range []string{"inputs", "canonical_inputs"} {
		for _, value := range evidence[kind].([]any) {
			input := value.(map[string]any)
			path, hash := input["path"].(string), pythonDependenciesAcceptShort(input["sha256"].(string))
			var identified bool
			for _, line := range strings.Split(md, "\n") {
				if strings.Contains(line, path) && strings.Contains(line, hash) {
					identified = true
				}
			}
			if !identified {
				t.Errorf("receipt omits %s provenance %s %s:\n%s", kind, path, hash, md)
			}
		}
	}
	if !strings.Contains(strings.ToLower(md), "canonical") {
		t.Errorf("receipt must distinguish canonical installation files from original application inputs:\n%s", md)
	}
	for _, value := range evidence["artifacts"].([]any) {
		artifact := value.(map[string]any)
		for _, key := range []string{"name", "version", "filename", "sha256"} {
			want := pythonDependenciesAcceptShort(artifact[key].(string))
			if !strings.Contains(md, want) {
				t.Errorf("receipt omits resolved artifact %s %s=%q:\n%s", artifact["name"], key, want, md)
			}
		}
	}
}

func TestPythonFingerprintTracksResolvedDependencies(t *testing.T) {
	document := pythonDependenciesAcceptDocument()
	r := pythonDependenciesAcceptReport(t, document)
	original := verify.Fingerprint(r)
	attributed := pythonDependenciesAcceptAttributed(t, r)
	attributedOriginal := verify.Fingerprint(attributed)
	changes := []struct {
		name   string
		change func(map[string]any)
	}{
		{"artifact hash", func(e map[string]any) {
			e["artifacts"].([]any)[0].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("bc", 32)
		}},
		{"artifact version", func(e map[string]any) {
			e["artifacts"].([]any)[0].(map[string]any)["version"] = "3.11"
		}},
		{"platform", func(e map[string]any) { e["platform"] = "linux/arm64" }},
		{"referenced input hash", func(e map[string]any) {
			e["inputs"].([]any)[1].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("de", 32)
		}},
		{"root input hash", func(e map[string]any) {
			e["inputs"].([]any)[0].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("e3", 32)
		}},
		{"canonical constraints hash", func(e map[string]any) {
			e["canonical_inputs"].([]any)[0].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("f4", 32)
		}},
		{"canonical requirements hash", func(e map[string]any) {
			e["canonical_inputs"].([]any)[1].(map[string]any)["sha256"] = "sha256:" + strings.Repeat("05", 32)
		}},
	}
	for _, change := range changes {
		changed := pythonDependenciesAcceptDocument()
		change.change(pythonDependenciesAcceptEvidence(changed))
		if got := verify.Fingerprint(pythonDependenciesAcceptReport(t, changed)); got == original {
			t.Errorf("changing %s while retaining the recipe must change the fingerprint", change.name)
		}
		changedAttributed := pythonDependenciesAcceptAttributed(t, pythonDependenciesAcceptReport(t, changed))
		if got := verify.Fingerprint(changedAttributed); got == attributedOriginal || changedAttributed.Fingerprint != got {
			t.Errorf("component attribution must preserve %s and recompute the fingerprint", change.name)
		}
	}
	attributedJSON, err := json.Marshal(attributed)
	if err != nil {
		t.Fatal(err)
	}
	var attributedDocument map[string]any
	if err := json.Unmarshal(attributedJSON, &attributedDocument); err != nil {
		t.Fatal(err)
	}
	if got := attributedDocument["toolchain"].(map[string]any)["python_dependencies"]; !reflect.DeepEqual(got, pythonDependenciesAcceptEvidence(document)) {
		t.Errorf("component attribution loses application dependency evidence: got %s", attributedJSON)
	}
	if got := verify.Fingerprint(pythonDependenciesAcceptReport(t, attributedDocument)); got != attributedOriginal {
		t.Errorf("component JSON round trip changes the fingerprint: %s != %s", got, attributedOriginal)
	}

	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var roundtripDocument map[string]any
	if err := json.Unmarshal(encoded, &roundtripDocument); err != nil {
		t.Fatal(err)
	}
	gotEvidence := roundtripDocument["toolchain"].(map[string]any)["python_dependencies"]
	if !reflect.DeepEqual(gotEvidence, pythonDependenciesAcceptEvidence(document)) {
		t.Errorf("report JSON loses application dependency evidence: got %s", encoded)
	}
	roundtrip := pythonDependenciesAcceptReport(t, roundtripDocument)
	if got := verify.Fingerprint(roundtrip); got != original {
		t.Errorf("JSON round trip changes the fingerprint: %s != %s", got, original)
	}

	// Local Docker tags and immutable IDs select execution environments;
	// the portable evidence, including canonical bytes, identifies them.
	for _, image := range []string{"invariant-python-deps:local-only", "sha256:" + strings.Repeat("c6", 32)} {
		withImage := pythonDependenciesAcceptDocument()
		pythonDependenciesAcceptEvidence(withImage)["image"] = image
		selected := pythonDependenciesAcceptReport(t, withImage)
		pythonDependenciesAcceptSetImage(t, &selected, image)
		if got := verify.Fingerprint(selected); got != original {
			t.Errorf("local image %q changes the dependency fingerprint: %s != %s", image, got, original)
		}
		selectedJSON, err := json.Marshal(selected)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(selectedJSON), image) || strings.Contains(receipt.Markdown(&selected), image) {
			t.Errorf("receipt exposes a local dependency image identity %q: %s", image, selectedJSON)
		}
	}

	legacy := pythonDependenciesAcceptDocument()
	delete(legacy["toolchain"].(map[string]any), "python_dependencies")
	legacyReport := pythonDependenciesAcceptReport(t, legacy)
	legacyJSON, err := json.Marshal(legacyReport)
	if err != nil {
		t.Fatal(err)
	}
	var legacyDocument map[string]any
	if err := json.Unmarshal(legacyJSON, &legacyDocument); err != nil {
		t.Fatal(err)
	}
	legacyTools := legacyDocument["toolchain"].(map[string]any)
	if _, present := legacyTools["python_dependencies"]; present {
		t.Errorf("legacy report adds an empty Python dependency field: %s", legacyJSON)
	}
	if got, want := legacyTools["python_image"], legacy["toolchain"].(map[string]any)["python_image"]; got != want {
		t.Errorf("legacy Python image changed: got %v, want %v", got, want)
	}
	if got := verify.Fingerprint(legacyReport); got == original {
		t.Error("a report with resolved dependencies has the same fingerprint as the legacy report without them")
	}
}
