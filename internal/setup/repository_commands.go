package setup

import (
	"context"
	"encoding/json"
	"go/version"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/gitdek/invariant/internal/plumbing"
	"github.com/gitdek/invariant/internal/repository"
	"github.com/gitdek/invariant/internal/toolchain"
)

func (d *repositoryDraft) goCommands(p *repository.Part, files map[string][]byte) {
	p.Image = toolchain.GoImage
	seen := false
	for _, line := range strings.Split(string(files[path.Join(p.Directory, "go.mod")]), "\n") {
		line, _, _ = strings.Cut(line, "//")
		words := strings.Fields(line)
		if len(words) == 0 || words[0] != "go" && words[0] != "toolchain" {
			continue
		}
		if len(words) != 2 {
			p.Image = ""
			continue
		}
		if words[0] == "toolchain" && words[1] == "default" {
			continue
		}
		v := "go" + strings.TrimPrefix(words[1], "go")
		seen = seen || words[0] == "go"
		if !version.IsValid(v) || version.Compare(v, "go1.27.1") > 0 {
			p.Image = ""
		}
	}
	if !seen || !versionFiles(files, "go", "1.27.1") {
		p.Image = ""
	}
	if p.Image == "" {
		d.issue(p, "image", "go.mod/toolchain requirements are not compatible with the maintained Go 1.27.1 digest; select and verify a compatible pinned toolchain")
	}
	p.Commands["lint"] = repository.Command{Argv: []string{"go", "vet", "./..."}}
	p.Commands["build"] = repository.Command{Argv: []string{"go", "build", "./..."}}
	p.Commands["test"] = repository.Command{Argv: []string{"go", "test", "./..."}}
	p.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "{test}", "-count=1"}}
	// gofmt -l and -d report differences but do not fail on them.
}

type draftPackage struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Engines         map[string]string `json:"engines"`
	PackageManager  string            `json:"packageManager"`
	Jest            json.RawMessage   `json:"jest"`
}

func (d *repositoryDraft) nodeCommands(p *repository.Part, files map[string][]byte) error {
	var pkg draftPackage
	if err := plumbing.DecodeDependencyJSON(files[path.Join(p.Directory, "package.json")], &pkg); err != nil {
		d.issue(p, "inputs", "package.json is malformed or ambiguous; complete its manifest and commands")
		d.issue(p, "image", "package.json toolchain requirements could not be read; verify a compatible digest")
		return nil
	}
	p.Image = toolchain.NodeImage
	if !plumbing.CompatibleNodeVersion("24.21.0", pkg.Engines["node"]) || pkg.Engines["npm"] != "" || pkg.PackageManager != "" || !versionFiles(files, "node", "24.21.0") {
		p.Image = ""
		d.issue(p, "image", "package.json or version-file toolchain requirements are not known compatible with the maintained Node 24.21.0 digest; verify the Node and npm toolchains")
	}
	if pkg.PackageManager != "" {
		p.Commands["install"] = repository.Command{}
	}
	installed := func(name string) bool { return pkg.Dependencies[name] != "" || pkg.DevDependencies[name] != "" }
	for _, field := range []string{"format", "lint", "build", "test"} {
		script := pkg.Scripts[field]
		if field == "format" {
			if explicit, ok := pkg.Scripts["format:check"]; ok {
				script = explicit
			}
		}
		argv, ok := commandWords(script)
		if !ok || len(argv) == 0 {
			continue
		}
		valid := false
		switch field {
		case "format":
			valid = installed("prettier") && len(argv) >= 3 && argv[0] == "prettier" && slices.Contains([]string{"--check", "--list-different"}, argv[1]) && checkTargets(argv[2:])
		case "lint":
			valid = installed("eslint") && argv[0] == "eslint" && len(argv) >= 2 && checkTargets(argv[1:])
		case "build":
			valid = installed("typescript") && argv[0] == "tsc"
			project := "tsconfig.json"
			seen := map[string]bool{}
			for i := 1; valid && i < len(argv); i++ {
				flag := argv[i]
				if seen[flag] {
					valid = false
				}
				seen[flag] = true
				switch flag {
				case "--noEmit":
				case "-p", "--project":
					i++
					if i >= len(argv) || !repositoryPath(argv[i]) {
						valid = false
					} else {
						project = argv[i]
					}
				default:
					valid = false
				}
			}
			if valid {
				name := path.Join(p.Directory, project)
				valid = d.files[name]
				if valid {
					if err := d.tsconfig(p, name, map[string]bool{}); err != nil {
						return err
					}
				}
			}
		case "test":
			framework := argv[0]
			if !installed(framework) || framework != "vitest" && framework != "jest" {
				continue
			}
			one := append(slices.Clone(argv), "--testNamePattern", "{test}")
			candidate := *p
			candidate.Commands = map[string]repository.Command{"test_one": {Argv: one}}
			var adapter plumbing.RepositoryAcceptanceAdapter = plumbing.VitestRepositoryAcceptanceAdapter{}
			if framework == "jest" {
				adapter = plumbing.JestRepositoryAcceptanceAdapter{}
			}
			_, err := adapter.Select(context.Background(), candidate, plumbing.Test{Name: "draft", Says: "Draft selector", Part: p.Name, Framework: framework, File: path.Join(p.Directory, "draft.test.ts"), Selector: "draft"})
			valid = err == nil
			if valid {
				valid, err = d.nodeTestConfig(p, argv, framework, pkg.Jest)
				if err != nil {
					return err
				}
			}
			if valid {
				p.Commands["test_one"] = repository.Command{Argv: one}
			}
		}
		if valid {
			p.Commands[field] = repository.Command{Argv: argv}
		}
	}
	// Retain ancestor check policy too: formatters and linters can search up
	// from a nested part. Capture conservatively without evaluating overrides.
	for _, name := range d.names {
		if !encloses(path.Dir(name), p.Directory) {
			continue
		}
		base := path.Base(name)
		if base == "package.json" && path.Dir(name) != p.Directory {
			b, err := d.read(name)
			if err != nil {
				return err
			}
			var policy map[string]json.RawMessage
			if plumbing.DecodeDependencyJSON(b, &policy) != nil || policy == nil {
				d.issue(p, "inputs", name+": review inherited formatter/linter policy and its ownership")
			} else if policy["prettier"] != nil || policy["eslintConfig"] != nil {
				p.Inputs = append(p.Inputs, name)
			}
		}
		if path.Dir(name) == p.Directory && strings.HasPrefix(base, "tsconfig") && strings.HasSuffix(base, ".json") || strings.HasPrefix(base, "eslint.config.") || strings.HasPrefix(base, ".eslintrc") || strings.HasPrefix(base, "prettier.config.") || strings.HasPrefix(base, ".prettierrc") || base == ".prettierignore" || base == ".eslintignore" || base == ".editorconfig" {
			p.Inputs = append(p.Inputs, name)
		}
	}
	return nil
}

func (d *repositoryDraft) nodeTestConfig(p *repository.Part, argv []string, framework string, packageConfig json.RawMessage) (bool, error) {
	unresolved := func(name, reason string) (bool, error) {
		d.issue(p, "test", name+": "+reason+"; complete the test configuration for the native check and review test/test_one")
		return false, nil
	}
	config := ""
	for i, arg := range argv {
		if arg == "--config" {
			config = argv[i+1] // the native selector already validated flags
		} else if strings.HasPrefix(arg, "--config=") {
			config = strings.TrimPrefix(arg, "--config=")
		}
	}
	if name := path.Join(p.Directory, config); config != "" && d.files[name] {
		p.Inputs = append(p.Inputs, name)
	}
	var configs, workspaces []string
	for _, name := range d.names {
		if path.Dir(name) != p.Directory {
			continue
		}
		base := path.Base(name)
		if framework == "vitest" && strings.HasPrefix(base, "vitest.workspace.") {
			p.Inputs = append(p.Inputs, name)
			workspaces = append(workspaces, name)
		}
		if strings.HasPrefix(base, framework+".config.") || framework == "vitest" && strings.HasPrefix(base, "vite.config.") {
			p.Inputs = append(p.Inputs, name)
			configs = append(configs, name)
		}
	}
	if len(workspaces) > 0 {
		return unresolved(strings.Join(workspaces, ", "), "automatic Vitest workspace policy is unsupported")
	}
	if config != "" {
		configs = []string{path.Join(p.Directory, config)}
	} else if len(configs) > 1 || len(configs) > 0 && framework == "jest" && len(packageConfig) > 0 {
		if framework == "jest" && len(packageConfig) > 0 {
			configs = append(configs, path.Join(p.Directory, "package.json")+" jest")
		}
		return unresolved(strings.Join(configs, ", "), "ambiguous framework configuration; select one explicitly with --config")
	}
	if len(configs) == 0 && framework == "jest" && len(packageConfig) > 0 {
		if reason := nodeTestSettings(packageConfig, framework); reason != "" {
			return unresolved(path.Join(p.Directory, "package.json")+" jest", reason)
		}
	}
	for _, name := range configs {
		if !d.files[name] {
			return unresolved(name, "missing regular configuration evidence")
		}
		p.Inputs = append(p.Inputs, name)
		if config == "" && strings.HasPrefix(path.Base(name), "vite.config.") {
			return unresolved(name, "the native check requires an explicit supported Vitest configuration")
		}
		b, err := d.read(name)
		if err != nil {
			return false, err
		}
		if path.Ext(name) != ".json" {
			return unresolved(name, "drafting requires statically understood JSON test configuration")
		}
		if reason := nodeTestSettings(b, framework); reason != "" {
			return unresolved(name, reason)
		}
	}
	return true, nil
}

// A literal subset of the native runners' settings. Transforms, regular
// expressions and compiler options need review rather than a second interpreter.
func nodeTestSettings(data []byte, section string) string {
	var object map[string]json.RawMessage
	if plumbing.DecodeDependencyJSON(data, &object) != nil || object == nil {
		return section + " configuration must be an unambiguous JSON object"
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		value := object[key]
		valid := false
		switch {
		case section == "vitest" && key == "test":
			if reason := nodeTestSettings(value, "vitest.test"); reason != "" {
				return reason
			}
			valid = true
		case section == "jest" && key == "testEnvironment" || section == "vitest.test" && key == "environment":
			var environment string
			valid = json.Unmarshal(value, &environment) == nil && environment == "node"
		case section == "jest" && slices.Contains([]string{"clearMocks", "resetMocks", "restoreMocks"}, key) || section == "vitest.test" && key == "globals":
			valid = string(value) == "true" || string(value) == "false"
		case (section == "jest" || section == "vitest.test") && key == "testTimeout" || section == "vitest.test" && key == "hookTimeout":
			var timeout *uint32
			valid = json.Unmarshal(value, &timeout) == nil && timeout != nil && *timeout <= 2147483647
		case section == "jest" && slices.Contains([]string{"testMatch", "moduleFileExtensions"}, key) || section == "vitest.test" && slices.Contains([]string{"include", "exclude"}, key):
			var values []string
			valid = json.Unmarshal(value, &values) == nil && values != nil
			for _, v := range values {
				valid = valid && v != "" && !strings.ContainsAny(v, "\x00\r\n")
			}
		default:
			return section + "." + key + " is not a statically supported native-check setting"
		}
		if !valid {
			return section + "." + key + " has an unsupported native-check value"
		}
	}
	return ""
}

func checkTargets(targets []string) bool {
	for _, target := range targets {
		if target == "." {
			continue
		}
		if target == "" || strings.HasPrefix(target, "-") || strings.HasPrefix(target, "!") || path.IsAbs(target) || strings.ContainsAny(target, "\\:\x00\r\n{}") {
			return false
		}
		for _, component := range strings.Split(target, "/") {
			if component == ".." || component == ".git" {
				return false
			}
		}
	}
	return len(targets) > 0
}

// Only literal shell words are understood. Expansions, operators and unquoted
// globs are refused rather than changed into arguments with different meaning.
func commandWords(text string) ([]string, bool) {
	var words []string
	var word strings.Builder
	var quote rune
	started, escape := false, false
	for _, r := range text {
		if r == '\x00' || r == '\n' || r == '\r' || r == '$' || r == '`' {
			return nil, false
		}
		if escape {
			if quote == '"' && r != '"' && r != '\\' || r != ' ' && r != '\'' && r != '"' && r != '\\' {
				return nil, false
			}
			word.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escape, started = true, true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote, started = r, true
		} else if unicode.IsSpace(r) {
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		} else if strings.ContainsRune(";&|<>(){}*?[]#!~", r) {
			return nil, false
		} else {
			started = true
			word.WriteRune(r)
		}
	}
	if quote != 0 || escape {
		return nil, false
	}
	if started {
		words = append(words, word.String())
	}
	return words, len(words) > 0
}

func (d *repositoryDraft) pythonCommands(p *repository.Part, files map[string][]byte) error {
	p.Image = toolchain.PythonImage
	metadata, ok := literalTables(string(files[path.Join(p.Directory, "pyproject.toml")]))
	constraint := ""
	if raw, exists := metadata["project.requires-python"]; exists {
		values, valid := literalStrings(raw)
		ok = ok && valid && len(values) == 1
		if len(values) == 1 {
			constraint = values[0]
		}
	}
	if !ok || !pythonVersion(constraint) || !versionFiles(files, "python", toolchain.PythonVersion) {
		p.Image = ""
		d.issue(p, "image", "pyproject.toml/version-file toolchain requirements are unsupported or incompatible with the maintained Python "+toolchain.PythonVersion+" digest; verify a compatible toolchain")
	}
	locked := map[string]bool{}
	for _, name := range p.Lockfiles {
		body := strings.ReplaceAll(string(files[name]), "\\\n", " ")
		for _, line := range strings.Split(body, "\n") {
			line, _, _ = strings.Cut(line, "#")
			fields := strings.Fields(line)
			if len(fields) > 0 {
				pkg, _, _ := strings.Cut(fields[0], "==")
				locked[strings.ToLower(pkg)] = true
			}
		}
	}
	if locked["ruff"] {
		p.Commands["format"] = repository.Command{Argv: []string{"ruff", "format", "--check", "."}}
		p.Commands["lint"] = repository.Command{Argv: []string{"ruff", "check", "."}}
	}
	// Both runners can find policy above a requirements-only part. Retain
	// ancestors conservatively; inherited pytest roots need explicit review.
	for _, name := range d.names {
		if !encloses(path.Dir(name), p.Directory) {
			continue
		}
		switch path.Base(name) {
		case "ruff.toml", ".ruff.toml":
			p.Inputs = append(p.Inputs, name)
			continue
		case "pyproject.toml":
			p.Inputs = append(p.Inputs, name)
			if path.Dir(name) == p.Directory {
				continue
			}
		case "pytest.ini", ".pytest.ini", "tox.ini", "setup.cfg", "pytest.toml", ".pytest.toml":
			p.Inputs = append(p.Inputs, name)
		default:
			continue
		}
		// These policies are retained, but this draft grammar does not guess
		// their precedence or override a scope with an invented default.
		ok = false
		d.issue(p, "test", name+": pytest configuration or inherited scope is unsupported; review test/test_one and select a supported configuration")
	}
	if locked["pytest"] && ok {
		var targets, options []string
		if raw, exists := metadata["tool.pytest.ini_options.testpaths"]; exists {
			targets, ok = literalStrings(raw)
			if !strings.HasPrefix(strings.TrimSpace(raw), "[") {
				ok = false
				d.issue(p, "test", "pyproject.toml testpaths: scalar parsing is unsupported; declare a literal array with one pytest target per entry and review test/test_one")
			}
			if len(targets) == 0 || !checkTargets(targets) {
				ok = false
			}
			for _, target := range targets {
				if target != "." && !repositoryPath(target) {
					ok = false
				}
			}
		}
		if raw, exists := metadata["tool.pytest.ini_options.addopts"]; exists {
			values, valid := literalStrings(raw)
			if !valid {
				ok = false
			} else if strings.HasPrefix(strings.TrimSpace(raw), "[") {
				options = values
			} else if len(values) == 1 && values[0] != "" {
				var valid bool
				options, valid = commandWords(values[0])
				ok = ok && valid
			}
		}
		for _, option := range options {
			if option == "-c" || strings.HasPrefix(option, "-c=") {
				ok = false // an alternative configuration needs its own review
			}
		}
		argv := append([]string{"python3", "-m", "pytest"}, options...)
		one := append(slices.Clone(argv), "{test}")
		candidate := *p
		candidate.Commands = map[string]repository.Command{"test_one": {Argv: one}}
		_, err := (plumbing.PytestRepositoryAcceptanceAdapter{}).Select(context.Background(), candidate, plumbing.Test{Name: "draft", Says: "Draft selector", Part: p.Name, Framework: "pytest", File: path.Join(p.Directory, "test_draft.py"), Selector: "test_draft.py::test_draft"})
		if ok && err == nil {
			// Pytest reads addopts from the retained configuration itself. The
			// validation above must not duplicate flags such as -q at runtime.
			p.Commands["test"] = repository.Command{Argv: append([]string{"python3", "-m", "pytest"}, targets...)}
			p.Commands["test_one"] = repository.Command{Argv: []string{"python3", "-m", "pytest", "{test}"}}
		}
	}
	return nil
}

var tableName = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)
var scalarLiteral = regexp.MustCompile(`^(?:true|false|[+-]?[0-9]+(?:\.[0-9]+)?)$`)

// This is intentionally a small literal subset, not a TOML interpreter. An
// unsupported form leaves the draft incomplete rather than hiding metadata.
func literalTables(text string) (map[string]string, bool) {
	values := map[string]string{}
	sections := map[string]bool{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		var quote rune
		escape := false
		for i, r := range line {
			if escape {
				escape = false
			} else if r == '\\' && quote == '"' {
				escape = true
			} else if quote != 0 {
				if r == quote {
					quote = 0
				}
			} else if r == '\'' || r == '"' {
				quote = r
			} else if r == '#' {
				line = line[:i]
				break
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return values, false
			}
			section = line[1 : len(line)-1]
			if !tableName.MatchString(section) || sections[section] {
				return values, false
			}
			sections[section] = true
			continue
		}
		key, value, found := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !found || !tableName.MatchString(key) || strings.Contains(key, ".") {
			return values, false
		}
		key = section + "." + key
		if _, exists := values[key]; exists {
			return values, false
		}
		if _, ok := literalStrings(value); !ok && !scalarLiteral.MatchString(value) {
			return values, false
		}
		values[key] = value
	}
	return values, true
}

func literalStrings(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	array := strings.HasPrefix(raw, "[")
	if array {
		if !strings.HasSuffix(raw, "]") {
			return nil, false
		}
		raw = strings.TrimSpace(raw[1 : len(raw)-1])
	}
	values := []string{}
	for raw != "" {
		q := raw[0]
		if q != '\'' && q != '"' {
			return nil, false
		}
		end := 1
		for end < len(raw) && raw[end] != q {
			if raw[end] == '\\' && q == '"' {
				end++
			}
			end++
		}
		if end >= len(raw) {
			return nil, false
		}
		value := raw[1:end]
		if q == '"' {
			var err error
			value, err = strconv.Unquote(raw[:end+1])
			if err != nil {
				return nil, false
			}
		}
		values = append(values, value)
		raw = strings.TrimSpace(raw[end+1:])
		if raw == "" {
			break
		}
		if !array || raw[0] != ',' {
			return nil, false
		}
		raw = strings.TrimSpace(raw[1:])
	}
	return values, array || len(values) == 1
}

func versionFiles(files map[string][]byte, language, pinned string) bool {
	compatible := compatibleVersion
	if language == "node" {
		compatible = plumbing.CompatibleNodeVersion
	}
	for name, b := range files {
		base := path.Base(name)
		matches := base == "."+language+"-version" || language == "node" && base == ".nvmrc"
		if matches && !compatible(pinned, "="+strings.TrimPrefix(strings.TrimSpace(string(b)), "v")) {
			return false
		}
		if base == ".tool-versions" {
			for _, line := range strings.Split(string(b), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 0 || fields[0] != language && !(language == "node" && fields[0] == "nodejs") && !(language == "go" && fields[0] == "golang") {
					continue
				}
				if len(fields) != 2 || !compatible(pinned, "="+fields[1]) {
					return false
				}
			}
		}
	}
	return true
}

var versionNumber = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,2}$`)

func pythonVersion(constraint string) bool {
	var terms []string
	if strings.TrimSpace(constraint) == "" {
		return true
	}
	for _, term := range strings.Split(constraint, ",") {
		term = strings.TrimSpace(term)
		op := ""
		for len(term) > 0 && strings.ContainsRune("<>=!", rune(term[0])) {
			op += term[:1]
			term = term[1:]
		}
		term = strings.TrimSpace(term)
		if !versionNumber.MatchString(term) || !slices.Contains([]string{"==", "!=", ">=", "<=", ">", "<"}, op) {
			return false
		}
		// PEP 440 pads release segments: ==3.13 means 3.13.0, not 3.13.*.
		for strings.Count(term, ".") < 2 {
			term += ".0"
		}
		terms = append(terms, op+term)
	}
	return compatibleVersion(toolchain.PythonVersion, strings.Join(terms, " "))
}

// A conjunction of numeric bounds is sufficient for the maintained runtimes.
// Other version languages require review instead of an assumed compatibility.
func compatibleVersion(pinned, constraint string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	parse := func(s string) ([]int, bool) {
		if !versionNumber.MatchString(s) {
			return nil, false
		}
		var numbers []int
		for _, word := range strings.Split(s, ".") {
			n, err := strconv.Atoi(word)
			if err != nil {
				return nil, false
			}
			numbers = append(numbers, n)
		}
		return numbers, true
	}
	actual, _ := parse(pinned)
	for _, term := range strings.Fields(strings.ReplaceAll(constraint, ",", " ")) {
		op := ""
		for len(term) > 0 && strings.ContainsRune("<>=!", rune(term[0])) {
			op += term[:1]
			term = term[1:]
		}
		bound, ok := parse(term)
		if !ok {
			return false
		}
		cmp := 0
		for i, n := range bound {
			if actual[i] < n {
				cmp = -1
				break
			}
			if actual[i] > n {
				cmp = 1
				break
			}
		}
		match := false
		switch op {
		case "", "=", "==":
			match = cmp == 0
		case "!=":
			match = cmp != 0
		case ">=":
			match = cmp >= 0
		case ">":
			match = cmp > 0
		case "<=":
			match = cmp <= 0
		case "<":
			match = cmp < 0
		}
		if !match {
			return false
		}
	}
	return true
}
