package receipt_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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
	md := receipt.Markdown(&r)
	evidence := pythonDependenciesAcceptEvidence(document)
	appRecipe := pythonDependenciesAcceptShort(evidence["recipe"].(string))
	naginiRecipe := pythonDependenciesAcceptShort(document["toolchain"].(map[string]any)["nagini_recipe"].(string))
	for _, want := range []string{appRecipe, pythonDependenciesAcceptShort(evidence["base"].(string)), naginiRecipe} {
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
	for _, value := range evidence["artifacts"].([]any) {
		artifact := value.(map[string]any)
		for _, key := range []string{"name", "version", "sha256"} {
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
	}
	for _, change := range changes {
		changed := pythonDependenciesAcceptDocument()
		change.change(pythonDependenciesAcceptEvidence(changed))
		if got := verify.Fingerprint(pythonDependenciesAcceptReport(t, changed)); got == original {
			t.Errorf("changing %s while retaining the recipe must change the fingerprint", change.name)
		}
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

	// Local Docker tags are execution details, not portable environment identity.
	withLocalTag := pythonDependenciesAcceptDocument()
	pythonDependenciesAcceptEvidence(withLocalTag)["image"] = "invariant-python-deps:local-only"
	tagged := pythonDependenciesAcceptReport(t, withLocalTag)
	if got := verify.Fingerprint(tagged); got != original {
		t.Errorf("local image tag changes the dependency fingerprint: %s != %s", got, original)
	}
	taggedJSON, err := json.Marshal(tagged)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(taggedJSON), "invariant-python-deps:local-only") {
		t.Errorf("report JSON exposes a local dependency image tag: %s", taggedJSON)
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
