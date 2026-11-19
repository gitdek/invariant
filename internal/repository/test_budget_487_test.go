package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// #487: a part can declare a longer test budget for the factory's checks of
// it, in whole minutes, up to the most a check allows. A part that declares
// none keeps the default.

func TestAPartDeclaresALongerTestBudget487(t *testing.T) {
	app := part461("app", ".", "go")
	app["test_minutes"] = 30
	root := repo461(t, contract461(app, part461("web", "web", "typescript")))
	s := load461(t, root, commit461(t, root))
	parts := s.Config().Parts
	if parts[0].TestMinutes != 30 || parts[1].TestMinutes != 0 {
		t.Fatalf("declared test budgets are %d and %d minutes, want 30 and none", parts[0].TestMinutes, parts[1].TestMinutes)
	}
	selected, err := s.PartsFor([]string{"main.go"})
	if err != nil || len(selected) != 1 || selected[0].TestMinutes != 30 {
		t.Fatalf("a selected part's budget = %+v, %v", selected, err)
	}
	b, err := json.Marshal(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"test_minutes":30`) {
		t.Errorf("the part serializes as %s", b)
	}
	for _, minutes := range []int{DefaultTestMinutes, MaxTestMinutes} {
		app["test_minutes"] = minutes
		root := repo461(t, contract461(app))
		if got := load461(t, root, commit461(t, root)).Config().Parts[0].TestMinutes; got != minutes {
			t.Errorf("a part that declares %d minutes has %d", minutes, got)
		}
	}
}

// The file is refused when it loads, naming what the part declared and the
// most a check allows.
func TestATestBudgetPastTheMostIsRefused487(t *testing.T) {
	for _, c := range []struct {
		name  string
		raw   string
		wants []string
	}{
		{"past the most", `"test_minutes":90`, []string{`part "app" declares test_minutes 90`, "the most a check allows, 55"}},
		{"one past the most", `"test_minutes":56`, []string{"test_minutes 56", "55"}},
		{"shorter than the default", `"test_minutes":5`, []string{"test_minutes 5", "the default 10 minutes"}},
		{"zero", `"test_minutes":0`, []string{"test_minutes 0"}},
		{"negative", `"test_minutes":-30`, []string{"test_minutes -30"}},
		{"a fraction", `"test_minutes":30.5`, []string{"cannot unmarshal"}},
		{"a duration", `"test_minutes":"30m"`, []string{"cannot unmarshal"}},
		{"null", `"test_minutes":null`, []string{"must not be null"}},
		{"twice", `"test_minutes":30,"test_minutes":30`, []string{`duplicate field "test_minutes"`}},
		{"another spelling", `"test_budget":30`, []string{`unknown field "test_budget"`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := repo461(t, contract461(part461("app", ".", "go")))
			text := json461(t, contract461(part461("app", ".", "go")))
			text = strings.Replace(text, `"directory":".",`, `"directory":".",`+c.raw+`,`, 1)
			write461(t, root, contractPath461, text)
			_, err := Load(t.Context(), root, commit461(t, root))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Load = %v, want ErrInvalid", err)
			}
			for _, want := range c.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Load = %v, want it to say %q", err, want)
				}
			}
		})
	}
}

// A contract without a declared budget serializes as it did before them, so
// the execution digests a ratified plan holds don't change.
func TestAContractWithoutTestBudgetsSerializesAsBefore487(t *testing.T) {
	root := repo461(t, contract461(part461("app", ".", "go"), part461("worker", "worker", "python")))
	s := load461(t, root, commit461(t, root))
	b, err := json.Marshal(s.Config())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "test_minutes") {
		t.Errorf("a contract without test budgets has some: %s", b)
	}
}
