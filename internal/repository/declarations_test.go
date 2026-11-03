package repository

import (
	"strings"
	"testing"
)

func TestRepositoryContractStrictNestedJSON(t *testing.T) {
	c := mixed461()
	valid := json461(t, c)
	for _, tc := range []struct {
		name, old, replacement string
	}{
		{"case alias", `"version":1`, `"version":1,"Version":1`},
		{"duplicate check", `"lint":{"argv":["go","vet","./..."]}`, `"lint":{"argv":["go","vet","./..."]},"lint":{"argv":["true"]}`},
		{"duplicate argv", `"argv":["go","vet","./..."]`, `"argv":["go","vet","./..."],"argv":["true"]`},
		{"duplicate shared key", `"path":"shared/"`, `"path":"shared/","path":"shared/"`},
		{"null argument", `"go","vet","./..."`, `"go",null,"./..."`},
		{"null field with reason", `"argv":["go","vet","./..."]`, `"argv":null,"not_applicable":"Skipped"`},
		{"two selectors in one argument", `"^{test}$"`, `"{test}{test}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(valid, tc.old, tc.replacement, 1)
			if text == valid {
				t.Fatal("fixture did not change")
			}
			if _, err := decodeConfig([]byte(text)); err == nil {
				t.Fatal("invalid declaration was accepted")
			}
		})
	}
}

func TestRepositoryContractInapplicableNamedTest(t *testing.T) {
	p := part461("app", ".", "go")
	p["commands"].(map[string]any)["test_one"] = map[string]any{"not_applicable": "Only the complete suite can run."}
	c := contract461(p)
	root := repo461(t, c)
	sameConfig461(t, load461(t, root, commit461(t, root)).Config(), c)
}

func TestRepositoryContractRejectsInvalidUTF8(t *testing.T) {
	text := json461(t, mixed461())
	text = strings.Replace(text, "tools/", "tools/\xff/", 1)
	if _, err := decodeConfig([]byte(text)); err == nil {
		t.Fatal("invalid UTF-8 was replaced in the configuration")
	}
}
