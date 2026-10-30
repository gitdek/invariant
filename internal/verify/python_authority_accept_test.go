package verify

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// The pinned-base parser's versioned JSON boundary is deliberately observable:
// Every listed field is required and non-null, including fields whose value
// may be an empty string, collection or false. Check presence before semantic
// policy and name an incomplete field in the diagnostic.
// Testing only a second parser beside production cannot prove that production
// uses pip at all. These types describe its report, not an independent parser.
// Paths are project-relative and slash-separated; include values retain the
// spelling pip actually followed. Requirement identity comes from pip's own
// InstallRequirement constructor, including its separate link and marker.
type pyDepsAuthorityReport struct {
	Schema       int                          `json:"schema"`
	PipVersion   string                       `json:"pip_version"`
	Root         string                       `json:"root"`
	Files        []pyDepsAuthorityFile        `json:"files"`
	Includes     []pyDepsAuthorityInclude     `json:"includes"`
	Requirements []pyDepsAuthorityRequirement `json:"requirements"`
	Options      []pyDepsAuthorityOption      `json:"options"`
	Errors       []pyDepsAuthorityError       `json:"errors"`
}

type pyDepsAuthorityFile struct {
	Path       string `json:"path"`
	Constraint bool   `json:"constraint"`
}

type pyDepsAuthorityInclude struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Value      string `json:"value"`
	Constraint bool   `json:"constraint"`
}

type pyDepsAuthorityRequirement struct {
	Source     string                  `json:"source"`
	Line       int                     `json:"line"`
	Name       string                  `json:"name"`
	Extras     []string                `json:"extras"`
	Specifier  string                  `json:"specifier"`
	Marker     string                  `json:"marker"`
	Hashes     map[string][]string     `json:"hashes"`
	Constraint bool                    `json:"constraint"`
	URL        string                  `json:"url"`
	Link       string                  `json:"link"`
	Path       string                  `json:"path"`
	Archive    bool                    `json:"archive"`
	Editable   bool                    `json:"editable"`
	Options    []pyDepsAuthorityOption `json:"options"`
}

type pyDepsAuthorityOption struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type pyDepsAuthorityError struct {
	Source  string `json:"source"`
	Line    int    `json:"line"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func pyDepsAuthorityFixtureReport() pyDepsAuthorityReport {
	return pyDepsAuthorityReport{
		Schema: 1, PipVersion: "26.2.1", Root: "requirements.txt",
		Files: []pyDepsAuthorityFile{{"requirements.txt", false}, {"deps/application.txt", false}, {"deps/constraints.txt", true}},
		Includes: []pyDepsAuthorityInclude{
			{"requirements.txt", "deps/application.txt", "deps/application.txt", false},
			{"deps/application.txt", "deps/constraints.txt", "constraints.txt", true},
		},
		Requirements: []pyDepsAuthorityRequirement{
			{Source: "deps/application.txt", Line: 1, Name: "packaging", Extras: []string{}, Specifier: ">=24,<25", Hashes: map[string][]string{}, Options: []pyDepsAuthorityOption{}},
			{Source: "deps/constraints.txt", Line: 1, Name: "packaging", Extras: []string{}, Specifier: "==24.2", Hashes: map[string][]string{}, Constraint: true, Options: []pyDepsAuthorityOption{}},
		},
		Options: []pyDepsAuthorityOption{}, Errors: []pyDepsAuthorityError{},
	}
}

// #264: every example is superficially valid to an overly broad Go name or
// whitespace parser. Retain all prior strict rejection tests, and add these
// in root requirements, nested requirements and nested constraints. A strict
// preflight may refuse them before pip; either path must prevent installation.
func TestPythonDependenciesRejectPinnedPipInterpretationGaps(t *testing.T) {
	cases := []pyDepsInvalidInputCase{
		{"review bare sdist", "package-1.0.tar.gz\n"},
		{"review bare wheel", "packaging-24.2-py3-none-any.whl\n"},
		{"bare zip", "package-1.0.zip\n"},
		{"bare compressed tar", "package-1.0.tar.bz2\n"},
		{"bare archive with marker", "package-1.0.tar.gz; python_version < '0'\n"},
		{"bare wheel with hash", "packaging-24.2-py3-none-any.whl --hash=sha256:" + strings.Repeat("a", 64) + "\n"},
		{"review empty requirement equals", "--requirement= deps/application.txt\n"},
		{"empty constraint equals", "--constraint= deps/application.txt\n"},
		{"review no-break-space requirement", "-r\u00a0deps/application.txt\n"},
		{"no-break-space constraint", "-c\u00a0deps/application.txt\n"},
	}
	for _, tc := range cases {
		for _, filename := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
			t.Run(tc.name+"/"+filename, func(t *testing.T) {
				pyDepsFakeDocker(t)
				root := pyDepsFixture(t)
				// If a faulty preflight strips the bad separator or empty value,
				// its repaired reference must really exist even in a nested file.
				pyDepsWrite(t, root, "deps/deps/application.txt", "packaging==24.2\n")
				pyDepsWrite(t, root, filename, tc.raw)
				before := pyDepsFiles(t, root)
				pyDepsAssertAuthorityRefused(t, root, false)
				if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
					t.Fatal("rejecting a pip interpretation gap changed application inputs")
				}
			})
		}
	}
}

func TestPythonDependenciesParseWithPinnedPipBeforeBuild(t *testing.T) {
	pyDepsFakeDocker(t)
	t.Setenv("INVARIANT_ACCEPT_CREDENTIAL", "host-only-secret")
	t.Setenv("PIP_INDEX_URL", "https://host-only.invalid/private")
	t.Setenv("PYTHONPATH", "/host/unsafe-python-path")
	root := pyDepsFixture(t)
	pyDepsWrite(t, root, "pip.py", "raise AssertionError('application pip module executed')\n")
	pyDepsWrite(t, root, "sitecustomize.py", "raise AssertionError('application sitecustomize executed')\n")
	before := pyDepsFiles(t, root)
	if recipe, err := toolchain.PythonDepsRecipe(root); err != nil || !pyDepsDigest.MatchString(recipe) {
		t.Fatalf("recipe could not obtain an authoritative offline report: %q %v", recipe, err)
	}
	pyDepsAssertParserCalls(t, root)
	if pyDepsBuildCount(t) != 0 {
		t.Fatal("requesting a recipe built a dependency image")
	}
	env := pyDepsBuild(t, root)
	pyDepsAssertCanonicalStaging(t, root, env, []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"})
	pyDepsAssertParserCalls(t, root)
	seenParser := false
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "parser" {
			seenParser = true
		}
		if call.Kind == "build" && !seenParser {
			t.Fatal("dependency build ran before the pinned pip parser")
		}
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("offline pinned pip parsing changed application files")
	}
	t.Run("no requirements keeps standard library path", func(t *testing.T) {
		pyDepsFakeDocker(t)
		empty := t.TempDir()
		if recipe, err := toolchain.PythonDepsRecipe(empty); err != nil || recipe != "" {
			t.Fatalf("missing requirements invoked dependency parsing: %q %v", recipe, err)
		}
		if env, err := toolchain.PythonDepsImage(context.Background(), empty); err != nil || env != nil {
			t.Fatalf("missing requirements selected dependency environment: %#v %v", env, err)
		}
		if calls := pyDepsCalls(t); len(calls) != 0 {
			t.Fatalf("standard library path invoked Docker: %#v", calls)
		}
	})
	if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
		t.Run("production pip reports every unsupported form", pyDepsAssertProductionParserReportsForbiddenForms)
	}
}

// The earlier rejection corpus deliberately fails strict preflight before
// Docker. To test the production Python helper's own observation of forbidden
// syntax, let that preflight read a valid project, then have the CLI boundary
// substitute a second read-only project when forwarding the actual helper to
// real Docker. No independent parser or fabricated report participates here.
func pyDepsAssertProductionParserReportsForbiddenForms(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		filename string
		body     string
		option   string
		form     string
	}{
		{"standalone index", "requirements.txt", "--index-url https://example.invalid/simple\n-r deps/application.txt\n", "--index-url", ""},
		{"standalone source builds", "deps/application.txt", "--no-binary :all:\npackaging>=24,<25\n-c constraints.txt\n", "--no-binary", ""},
		{"constraint option", "deps/constraints.txt", "--find-links https://example.invalid/wheels\npackaging==24.2\n", "--find-links", ""},
		{"standalone pre-release flag", "requirements.txt", "--pre\n-r deps/application.txt\n", "--pre", ""},
		{"per requirement build setting", "deps/application.txt", "packaging>=24,<25 --config-settings=backend.key=value\n-c constraints.txt\n", "--config-settings", ""},
		{"named wheel link", "requirements.txt", "packaging-24.2-py3-none-any.whl\n", "", "link"},
		{"bare source archive", "deps/application.txt", "package-1.0.tar.gz\n", "", "link"},
		{"named URL link", "deps/application.txt", "packaging @ https://example.invalid/packaging-24.2-py3-none-any.whl ; python_version < '0'\n", "", "link"},
		{"editable local directory", "requirements.txt", "-e ./local-package\n", "", "editable"},
		{"empty equals include", "requirements.txt", "--requirement= deps/application.txt\n", "", "error"},
		{"no-break-space include", "requirements.txt", "-r\u00a0deps/application.txt\n", "", "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pyDepsFakeDocker(t)
			acceptedRoot, reportedRoot := pyDepsFixture(t), pyDepsFixture(t)
			pyDepsWrite(t, reportedRoot, tc.filename, tc.body)
			before := pyDepsFiles(t, reportedRoot)
			t.Setenv("INVARIANT_PY_DEPS_PARSER_PROJECT", reportedRoot)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if env, err := toolchain.PythonDepsImage(ctx, acceptedRoot); err == nil || env != nil {
				t.Errorf("production accepted a forbidden form reported by its actual pinned pip helper: env=%#v error=%v", env, err)
			}
			var report pyDepsAuthorityReport
			found := false
			for _, call := range pyDepsCalls(t) {
				if call.Kind == "build" || call.Kind == "application" || call.Kind == "evidence" {
					t.Errorf("real helper's forbidden form reached installation or execution: %#v", call)
				}
				if call.Kind == "parser" && len(call.ParserReport) > 0 {
					if err := json.Unmarshal(call.ParserReport, &report); err != nil {
						t.Fatal(err)
					}
					found = true
				}
			}
			if !found || report.Schema != 1 || report.PipVersion == "" {
				t.Fatalf("actual production helper failed without a pip report; infrastructure failure is not input refusal: %#v", report)
			}
			if tc.option != "" {
				options := append([]pyDepsAuthorityOption{}, report.Options...)
				for _, row := range report.Requirements {
					options = append(options, row.Options...)
				}
				observed := false
				for _, option := range options {
					observed = observed || (option.Name == tc.option && option.Source == tc.filename && option.Line > 0)
				}
				if !observed {
					t.Fatalf("pip accepted the option syntax but production omitted its option row: wanted %s in %s; report=%#v", tc.option, tc.filename, report)
				}
			} else {
				observed := false
				for _, inputError := range report.Errors {
					observed = observed || (inputError.Source == tc.filename && inputError.Type != "" && inputError.Message != "")
				}
				for _, row := range report.Requirements {
					if row.Source != tc.filename {
						continue
					}
					switch tc.form {
					case "link":
						observed = observed || row.Link != "" || row.URL != "" || row.Path != "" || row.Archive
					case "editable":
						observed = observed || row.Editable
					}
				}
				if !observed {
					t.Fatalf("production omitted pip's forbidden form or input refusal: wanted %s in %s; report=%#v", tc.form, tc.filename, report)
				}
			}
			if !reflect.DeepEqual(before, pyDepsFiles(t, reportedRoot)) {
				t.Fatal("the real production parser rewrote its read-only project")
			}
		})
	}
}

func pyDepsAssertParserCalls(t *testing.T, root string) {
	t.Helper()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, call := range pyDepsCalls(t) {
		if call.Kind != "parser" {
			continue
		}
		count++
		if call.Image != toolchain.PythonImage || call.Network != "none" || call.Platform != "linux/amd64" {
			t.Errorf("parser did not use the pinned offline base: %#v", call)
		}
		isolated := false
		for _, arg := range call.Command {
			isolated = isolated || arg == "-I"
		}
		python := call.Entrypoint
		if python == "" && len(call.Command) > 0 {
			python = call.Command[0]
		}
		if !isolated || !path.IsAbs(python) || !strings.HasPrefix(path.Base(python), "python") {
			t.Errorf("parser is not an absolute isolated Python invocation: %#v", call)
		}
		if call.Workdir == "" || !path.IsAbs(call.Workdir) {
			t.Errorf("parser lacks an explicit clean working directory: %#v", call)
		}
		projectMounted := false
		for target, host := range call.Mounts {
			if path.Clean(call.Workdir) == path.Clean(target) || strings.HasPrefix(path.Clean(call.Workdir), path.Clean(target)+"/") {
				t.Errorf("parser runs from mounted input directory %q: %#v", target, call)
			}
			resolvedHost, err := filepath.EvalSymlinks(host)
			if err == nil && filepath.Clean(resolvedHost) == filepath.Clean(resolvedRoot) {
				projectMounted = true
				if !call.ReadOnlyMounts[target] {
					t.Errorf("project is writable during authoritative parsing: %#v", call)
				}
			}
		}
		if !projectMounted {
			t.Errorf("parser did not read the application's project mount: %#v", call)
		}
		for key := range call.Environment {
			if key == "INVARIANT_ACCEPT_CREDENTIAL" || key == "PIP_INDEX_URL" || key == "PYTHONPATH" || key == "PYTHONHOME" {
				t.Errorf("parser inherited host configuration or credentials: %s", key)
			}
		}
	}
	if count == 0 {
		t.Fatal("production accepted application requirements without invoking pinned pip")
	}
}

func TestPythonDependenciesCanonicalFilesComeFromPipReport(t *testing.T) {
	// Deliberate boundary injection, never a real-pip fixture: keep source
	// bytes fixed while varying the successful trusted parser's semantic
	// report. This catches calling pip only for show, then using Go's parse.
	t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
	pyDepsFakeDocker(t)
	root := pyDepsFixture(t)
	before := pyDepsFiles(t, root)
	report := pyDepsAuthorityFixtureReport()
	pyDepsSetParserReport(t, report)
	first := pyDepsBuild(t, root)
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"}
	canonical := pyDepsAssertCanonicalStaging(t, root, first, inputs)
	pyDepsAssertCanonicalLines(t, canonical["requirements.txt"], []string{"packaging>=24,<25"})
	report.Requirements[0].Name = "requests"
	report.Requirements[0].Extras = []string{"security", "socks"}
	report.Requirements[0].Specifier = "==2.32.3"
	report.Requirements[0].Marker = "python_version < '0'"
	report.Requirements[0].Hashes = map[string][]string{"sha256": {strings.Repeat("a", 64), strings.Repeat("b", 64)}}
	pyDepsSetParserReport(t, report)
	second := pyDepsBuild(t, root)
	changed := pyDepsAssertCanonicalStaging(t, root, second, inputs)
	pyDepsAssertCanonicalLines(t, changed["requirements.txt"], []string{"requests[security,socks]==2.32.3; python_version < '0' --hash=sha256:" + strings.Repeat("a", 64) + " --hash=sha256:" + strings.Repeat("b", 64)})
	pyDepsAssertCanonicalLines(t, changed["constraints.txt"], []string{"packaging==24.2"})
	if first.Recipe == second.Recipe || first.Image == second.Image || reflect.DeepEqual(first.CanonicalInputs, second.CanonicalInputs) || pyDepsBuildCount(t) != 2 {
		t.Fatalf("pip report semantics did not determine canonical bytes and cache identity: first=%#v second=%#v", first, second)
	}
	if !reflect.DeepEqual(first.Inputs, second.Inputs) || !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("using pip's report altered original dependency evidence or application files")
	}
	if recipe, err := toolchain.PythonDepsRecipe(root); err != nil || recipe != second.Recipe {
		t.Fatalf("recipe API ignored the authoritative report: %q %v", recipe, err)
	}
}

func TestPythonDependenciesRejectDisallowedPipReports(t *testing.T) {
	// Every source file is an ordinary valid named requirement. Only the
	// trusted parser's report introduces the unsupported form or option.
	// In particular, a named req does not make a separate install.link safe.
	cases := []struct {
		name   string
		change func(*pyDepsAuthorityReport)
	}{
		{"named requirement with link", func(r *pyDepsAuthorityReport) { r.Requirements[0].Link = "file:///project/package.whl" }},
		{"named direct URL", func(r *pyDepsAuthorityReport) { r.Requirements[0].URL = "https://example.invalid/package.whl" }},
		{"local path", func(r *pyDepsAuthorityReport) { r.Requirements[0].Path = "deps/package" }},
		{"archive", func(r *pyDepsAuthorityReport) { r.Requirements[0].Archive = true }},
		{"editable", func(r *pyDepsAuthorityReport) { r.Requirements[0].Editable = true }},
		{"unnamed requirement", func(r *pyDepsAuthorityReport) { r.Requirements[0].Name = "" }},
		{"requirement from unreported file", func(r *pyDepsAuthorityReport) { r.Requirements[0].Source = "not-followed.txt" }},
		{"unsupported hash", func(r *pyDepsAuthorityReport) {
			r.Requirements[0].Hashes = map[string][]string{"md5": {strings.Repeat("a", 32)}}
		}},
		{"pip input error", func(r *pyDepsAuthorityReport) {
			r.Errors = []pyDepsAuthorityError{{"deps/application.txt", 1, "InstallationError", "Invalid requirement: acceptance parser refusal"}}
		}},
		{"escaped include", func(r *pyDepsAuthorityReport) {
			r.Includes[0].Target = "../outside.txt"
			r.Includes[0].Value = "../outside.txt"
		}},
		{"absolute include", func(r *pyDepsAuthorityReport) {
			r.Includes[0].Target = "/etc/passwd"
			r.Includes[0].Value = "/etc/passwd"
		}},
		{"remote include", func(r *pyDepsAuthorityReport) {
			r.Includes[0].Target = "https://example.invalid/req.txt"
			r.Includes[0].Value = r.Includes[0].Target
		}},
		{"empty include value", func(r *pyDepsAuthorityReport) { r.Includes[0].Value = "" }},
		{"unreported include target", func(r *pyDepsAuthorityReport) { r.Includes[0].Target = "not-followed.txt" }},
		{"escaped file", func(r *pyDepsAuthorityReport) { r.Files[1].Path = "../outside.txt" }},
		{"missing reported file", func(r *pyDepsAuthorityReport) { r.Files = r.Files[:1] }},
		{"unknown schema", func(r *pyDepsAuthorityReport) { r.Schema = 999 }},
		{"missing pip identity", func(r *pyDepsAuthorityReport) { r.PipVersion = "" }},
		{"wrong root", func(r *pyDepsAuthorityReport) { r.Root = "other.txt" }},
		{"missing requirement records", func(r *pyDepsAuthorityReport) { r.Requirements = nil }},
	}
	for _, name := range []string{"--index-url", "--extra-index-url", "--no-binary", "--find-links", "--trusted-host", "--no-index", "--pre", "--require-hashes", "--config-settings", "--unknown-option"} {
		option := name
		cases = append(cases, struct {
			name   string
			change func(*pyDepsAuthorityReport)
		}{"standalone " + option, func(r *pyDepsAuthorityReport) {
			r.Options = []pyDepsAuthorityOption{{"requirements.txt", 1, option, "reported"}}
		}})
	}
	cases = append(cases, struct {
		name   string
		change func(*pyDepsAuthorityReport)
	}{"per requirement option", func(r *pyDepsAuthorityReport) {
		r.Requirements[0].Options = []pyDepsAuthorityOption{{"deps/application.txt", 1, "--config-settings", "backend.key=value"}}
	}})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
			pyDepsFakeDocker(t)
			root := pyDepsFixture(t)
			before := pyDepsFiles(t, root)
			report := pyDepsAuthorityFixtureReport()
			tc.change(&report)
			pyDepsSetParserReport(t, report)
			pyDepsAssertAuthorityRefused(t, root, true)
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
				t.Fatal("rejecting pip's report changed application inputs")
			}
		})
	}
}

func TestPythonDependenciesParserFailuresDoNotBuildOrFallBack(t *testing.T) {
	for _, cached := range []bool{false, true} {
		for _, mode := range []string{"process failure", "empty report", "invalid JSON", "empty JSON object", "truncated report"} {
			name := mode
			if cached {
				name = "cached/" + name
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("INVARIANT_PYTHON_DEPS_REAL", "")
				pyDepsFakeDocker(t)
				root := pyDepsFixture(t)
				if cached {
					pyDepsBuild(t, root)
				}
				// Ignore the initial successful build; no failed parse may
				// build again, inspect cached evidence, or execute application.
				if err := os.WriteFile(filepath.Join(os.Getenv("INVARIANT_PY_DEPS_FAKE"), "calls.jsonl"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "process failure":
					t.Setenv("INVARIANT_PY_DEPS_PARSER_FAILURE", "acceptance pinned pip parser unavailable")
				case "empty report":
					pyDepsSetParserRawReport(t, nil)
				case "invalid JSON":
					pyDepsSetParserRawReport(t, []byte("pip parser emitted invalid JSON"))
				case "empty JSON object":
					pyDepsSetParserRawReport(t, []byte("{}"))
				case "truncated report":
					pyDepsSetParserRawReport(t, []byte(`{"schema":1,"pip_version":"26.2.1","requirements":[`))
				}
				pyDepsAssertAuthorityRefused(t, root, true)
			})
		}
	}
}

func pyDepsAssertAuthorityRefused(t *testing.T, root string, requireParser bool) {
	t.Helper()
	if recipe, err := toolchain.PythonDepsRecipe(root); err == nil || recipe != "" {
		t.Errorf("recipe API accepted refused or untrustworthy pip report: recipe=%q error=%v", recipe, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if env, err := toolchain.PythonDepsImage(ctx, root); err == nil || env != nil {
		t.Errorf("image API accepted refused or untrustworthy pip report: env=%#v error=%v", env, err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	b, evidence, err := (Python{Image: toolchain.PythonImage}).Check(ctx, p)
	if err == nil || b.Passed || len(evidence.Traces) != 0 {
		t.Errorf("parser refusal or failure ran a Python fallback: build=%#v evidence=%#v error=%v", b, evidence, err)
	}
	if os.Getenv("INVARIANT_PY_DEPS_PARSER_FAILURE") != "" && (err == nil || !strings.Contains(err.Error(), "acceptance pinned pip parser unavailable")) {
		t.Errorf("parser failure lost its actionable diagnostic: %v", err)
	}
	for _, call := range pyDepsCalls(t) {
		if call.Kind == "build" || call.Kind == "application" || call.Kind == "evidence" {
			t.Errorf("refused parsing reached installation, cached evidence or execution: %#v", call)
		}
	}
	if requireParser {
		pyDepsAssertParserCalls(t, root)
	}
}
