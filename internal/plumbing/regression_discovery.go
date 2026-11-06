package plumbing

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

type regressionDiscovery struct {
	framework string
	config    string
	targets   []string
	include   []string
	exclude   []string
	ignore    []*regexp.Regexp
	pyIgnore  []*regexp.Regexp
	python    []string
	recurse   []string
	valid     bool
}

func captureRegressionDiscovery(part repository.Part, base []string, read func(string) ([]byte, error)) *regressionDiscovery {
	d := &regressionDiscovery{python: []string{"test_*.py", "*_test.py"}, recurse: []string{".*", "build", "dist", "CVS", "_darcs", "{arch}", "*.egg", "venv", "node_modules"}}
	argv := part.Commands["test"].Argv
	if len(argv) == 0 || part.Commands["test"].NotApplicable != "" {
		return d
	}
	switch {
	case part.Language == "python" && argv[0] == "pytest":
		d.framework, argv = "pytest", argv[1:]
	case part.Language == "python" && len(argv) >= 3 && slices.Equal(argv[:3], []string{"python3", "-m", "pytest"}):
		d.framework, argv = "pytest", argv[3:]
	case part.Language == "typescript" && len(argv) >= 2 && slices.Equal(argv[:2], []string{"vitest", "run"}):
		d.framework, argv = "vitest", argv[2:]
	case part.Language == "typescript" && argv[0] == "jest":
		d.framework, argv = "jest", argv[1:]
	default:
		return d
	}
	if !d.arguments(argv) {
		return d
	}
	if d.config != "" && path.Dir(d.config) != "." {
		return d // configuration-relative roots need another discovery grammar
	}
	var configs []string
	var packageConfig map[string]any
	for _, file := range base {
		if !withinRegressionDir(file, part.Directory) {
			// Refuse inherited automatic policy whose root we cannot establish.
			if regressionPolicyFile(file) && withinRegressionDir(part.Directory, path.Dir(file)) && path.Dir(file) != part.Directory {
				return d
			}
			continue
		}
		name := path.Base(file)
		if d.framework == "pytest" && (name == "conftest.py" || name == "__init__.py") {
			return d // imports and collection hooks are executable policy
		}
		if d.framework != "pytest" && (strings.HasPrefix(name, ".babelrc") || strings.HasPrefix(name, "babel.config.")) {
			return d // automatic transformation can replace collected tests
		}
		if d.framework == "pytest" {
			switch name {
			case "pytest.ini", ".pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "pytest.toml", ".pytest.toml":
				if path.Dir(file) != part.Directory {
					return d // suite targets can select a nested configuration root
				}
				configs = append(configs, name)
			}
		} else {
			if name == "package.json" && d.framework == "jest" {
				data, err := read(file)
				var pkg map[string]json.RawMessage
				if err != nil || json.Unmarshal(data, &pkg) != nil {
					return d
				}
				if _, ok := pkg["babel"]; ok {
					return d // babel-jest also loads inline transformation policy
				}
				if raw, ok := pkg["jest"]; ok && path.Dir(file) == part.Directory {
					packageConfig, _ = regressionLiteral(string(raw))
					if packageConfig == nil {
						return d
					}
				}
			}
			if path.Dir(file) != part.Directory {
				continue
			}
			if strings.HasPrefix(name, "vitest.workspace.") {
				return d
			}
			prefix := "jest.config."
			if d.framework == "vitest" {
				prefix = "vitest.config."
			}
			if strings.HasPrefix(name, prefix) || d.framework == "vitest" && strings.HasPrefix(name, "vite.config.") {
				configs = append(configs, name)
			}
		}
	}
	if d.config == "" {
		if len(configs) > 1 || len(configs) > 0 && packageConfig != nil {
			return d
		}
		if len(configs) == 1 {
			d.config = configs[0]
		}
	}
	if d.config != "" {
		data, err := read(path.Join(part.Directory, d.config))
		if err != nil {
			return d
		}
		if d.framework == "pytest" {
			if !d.pytestConfig(string(data)) {
				return d
			}
		} else {
			trim := strings.TrimSpace(string(data))
			if path.Ext(d.config) == ".json" {
				if !json.Valid(data) {
					return d
				}
			} else if !strings.HasPrefix(trim, "export default") && !strings.HasPrefix(trim, "module.exports") {
				return d
			}
			object, ok := regressionLiteral(string(data))
			if !ok || !d.nodeConfig(object) {
				return d
			}
		}
	} else if packageConfig != nil && !d.nodeConfig(packageConfig) {
		return d
	}
	for _, pattern := range append(append(slices.Clone(d.include), d.exclude...), d.python...) {
		if _, ok := regressionGlob(pattern); !ok {
			return d
		}
	}
	for _, pattern := range d.recurse {
		if strings.ContainsAny(pattern, `\[]`) {
			return d
		}
	}
	d.valid = true
	return d
}

// Unknown options can select tests or load code. Only this small literal suite
// grammar is understood; test_one is deliberately not the regression suite.
func (d *regressionDiscovery) arguments(argv []string) bool {
	for i := 0; i < len(argv); i++ {
		arg, value, equals := strings.Cut(argv[i], "=")
		if !strings.HasPrefix(arg, "-") {
			if d.framework == "jest" || !regressionTarget(arg) || equals {
				return false
			}
			d.targets = append(d.targets, path.Clean(arg))
			continue
		}
		switch arg {
		case "-q", "-v", "-vv", "--quiet", "--verbose", "--disable-warnings", "--strict-markers", "--strict-config":
			if d.framework != "pytest" || equals {
				return false
			}
			continue
		case "--runInBand":
			if d.framework != "jest" || equals {
				return false
			}
			continue
		case "--config", "-c", "--ignore", "--ignore-glob", "--tb", "--color", "--testTimeout":
		default:
			return false
		}
		if !equals {
			i++
			if i == len(argv) {
				return false
			}
			value = argv[i]
		}
		switch arg {
		case "--config", "-c":
			if d.config != "" || !regressionTarget(value) || value == "." || d.framework == "pytest" && arg != "-c" || d.framework == "jest" && arg == "-c" {
				return false
			}
			d.config = path.Clean(value)
		case "--ignore", "--ignore-glob":
			if d.framework != "pytest" {
				return false
			}
			if arg == "--ignore-glob" {
				re, ok := regressionGlob(value)
				if !ok {
					return false
				}
				// Pytest uses fnmatch, whose stars cross directory boundaries.
				expression := strings.ReplaceAll(re.String(), "[^/]*", ".*")
				expression = strings.ReplaceAll(expression, "[^/]", ".")
				d.pyIgnore = append(d.pyIgnore, regexp.MustCompile(expression))
			} else {
				if !regressionTarget(value) {
					return false
				}
				value = path.Clean(value)
				if value == "." {
					return false
				}
				d.exclude = append(d.exclude, value+"/**")
				d.exclude = append(d.exclude, value)
			}
		case "--tb":
			if d.framework != "pytest" || !slices.Contains([]string{"auto", "long", "short", "line", "native", "no"}, value) {
				return false
			}
		case "--color":
			if d.framework != "pytest" || !slices.Contains([]string{"yes", "no", "auto"}, value) {
				return false
			}
		case "--testTimeout":
			if n, err := strconv.Atoi(value); err != nil || n <= 0 || d.framework == "pytest" {
				return false
			}
		}
	}
	return true
}

func regressionTarget(value string) bool {
	c := path.Clean(value)
	return value != "" && !path.IsAbs(c) && c != ".." && !strings.HasPrefix(c, "../") && !strings.ContainsAny(value, "\\:*?[]{}!\x00")
}

func (d *regressionDiscovery) pytestConfig(text string) bool {
	section, key := "", ""
	values := map[string]string{}
	seen := map[string]bool{}
	toml := path.Ext(d.config) == ".toml"
	want := "pytest"
	if toml {
		if path.Base(d.config) != "pyproject.toml" {
			return false
		}
		want = "tool.pytest.ini_options"
	} else if path.Base(d.config) == "setup.cfg" {
		want = "tool:pytest"
	}
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			continue
		}
		if strings.HasPrefix(trim, "[") {
			if !strings.HasSuffix(trim, "]") {
				return false
			}
			section, key = strings.TrimSuffix(strings.TrimPrefix(trim, "["), "]"), ""
			continue
		}
		if section != want {
			continue
		}
		if !toml && key != "" && len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && !strings.Contains(trim, "=") {
			values[key] += " " + trim
			continue
		}
		name, value, ok := strings.Cut(trim, "=")
		key = strings.TrimSpace(name)
		if !ok || seen[key] {
			return false
		}
		seen[key] = true
		switch key {
		case "testpaths", "python_files", "norecursedirs":
		default:
			return false
		}
		values[key] = strings.TrimSpace(value)
	}
	for key, value := range values {
		var words []string
		if toml {
			p := &regressionLiteralParser{text: value}
			v, ok := p.value()
			if !ok || strings.TrimSpace(p.text) != "" {
				return false
			}
			if text, ok := v.(string); ok {
				words = strings.Fields(text)
			} else if words, ok = regressionStrings(v); !ok {
				return false
			}
		} else {
			if strings.ContainsAny(value, "#;\"'") {
				return false
			}
			words = strings.Fields(value)
		}
		switch key {
		case "testpaths":
			if len(d.targets) == 0 {
				for _, word := range words {
					if !regressionTarget(word) {
						return false
					}
					d.targets = append(d.targets, path.Clean(word))
				}
			}
		case "python_files":
			d.python = words
		case "norecursedirs":
			d.recurse = words
		}
	}
	return true
}

func (d *regressionDiscovery) nodeConfig(object map[string]any) bool {
	if d.framework == "vitest" {
		for key := range object {
			if key != "test" {
				return false
			}
		}
		if test, exists := object["test"]; exists {
			var ok bool
			object, ok = test.(map[string]any)
			if !ok {
				return false
			}
		}
	}
	for key, value := range object {
		switch {
		case d.framework == "vitest" && (key == "include" || key == "exclude") || d.framework == "jest" && (key == "testMatch" || key == "testPathIgnorePatterns" || key == "roots"):
			words, ok := regressionStrings(value)
			if !ok {
				return false
			}
			if d.framework == "jest" {
				for i, word := range words {
					// Jest matches full paths. Only a rootDir anchor or a leading
					// globstar can establish inclusion from a part-relative path.
					if key == "testMatch" && !strings.HasPrefix(word, "<rootDir>/") && !strings.HasPrefix(word, "**/") {
						return false
					}
					words[i] = strings.TrimPrefix(word, "<rootDir>/")
				}
			}
			switch key {
			case "include", "testMatch":
				d.include = words
			case "exclude":
				d.exclude = words
			case "roots":
				for i, word := range words {
					if word == "<rootDir>" {
						words[i] = "."
					} else if !regressionTarget(word) {
						return false
					}
				}
				d.targets = words
			case "testPathIgnorePatterns":
				for _, word := range words {
					// Only literal path fragments, not a second regex dialect.
					if strings.ContainsAny(word, `\.^$*+?()[]{}|<>`) {
						return false
					}
					d.ignore = append(d.ignore, regexp.MustCompile(regexp.QuoteMeta(word)))
				}
			}
		case key == "globals" && d.framework == "vitest" || slices.Contains([]string{"clearMocks", "resetMocks", "restoreMocks"}, key):
			if _, ok := value.(bool); !ok {
				return false
			}
		case key == "testTimeout":
			if _, ok := value.(float64); !ok {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (d *regressionDiscovery) contains(file string) bool {
	if !d.valid || file == d.config || regressionPolicyFile(file) || strings.Contains(file, ".config.") || strings.Contains(file, ".conf.") || strings.HasSuffix(file, ".d.ts") {
		return false
	}
	for _, component := range strings.Split(file, "/") {
		if strings.HasPrefix(component, ".") || component == "node_modules" || component == "__pycache__" {
			return false
		}
	}
	base := path.Base(file)
	conventional := false
	if d.framework == "pytest" {
		conventional = strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) && regressionMatches(d.python, base)
		for at := file; at != "."; at = path.Dir(at) {
			for _, ignored := range d.pyIgnore {
				if ignored.MatchString(at) {
					return false
				}
			}
		}
		for _, dir := range strings.Split(path.Dir(file), "/") {
			for _, pattern := range d.recurse {
				if matched, _ := path.Match(pattern, dir); matched && dir != "." {
					return false
				}
			}
		}
	} else {
		ext := path.Ext(base)
		if slices.Contains([]string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}, ext) {
			stem := strings.TrimSuffix(base, ext)
			conventional = strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || d.framework == "jest" && strings.Contains("/"+file, "/__tests__/")
			// Jest 29 defaults discover js/jsx/ts/tsx; other extensions need
			// explicit testMatch and moduleFileExtensions, which we refuse.
			if d.framework == "jest" && !slices.Contains([]string{".js", ".jsx", ".ts", ".tsx"}, ext) {
				conventional = false
			}
		}
		if d.framework == "vitest" && (strings.Contains("/"+file, "/dist/") || strings.Contains("/"+file, "/cypress/") || strings.Contains("/"+file, "/test-results/")) {
			return false
		}
	}
	if !conventional || d.include != nil && !regressionMatches(d.include, file) {
		return false
	}
	for at := file; at != "."; at = path.Dir(at) {
		if regressionMatches(d.exclude, at) {
			return false
		}
	}
	for _, ignored := range d.ignore {
		if ignored.MatchString("/" + file) {
			return false
		}
	}
	if len(d.targets) == 0 {
		return d.framework != "jest" || d.targets == nil
	}
	for _, target := range d.targets {
		if withinRegressionDir(file, target) {
			return true
		}
	}
	return false
}

func regressionMatches(patterns []string, file string) bool {
	for _, pattern := range patterns {
		if re, ok := regressionGlob(pattern); ok && re.MatchString(file) {
			return true
		}
	}
	return false
}

// A deliberately small shared glob grammar: *, ?, and whole-component **.
// Unsupported patterns refuse discovery rather than approximating inclusion.
func regressionGlob(pattern string) (*regexp.Regexp, bool) {
	if pattern == "" || strings.ContainsAny(pattern, `\[]{}!()<>`) || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "..") {
		return nil, false
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				if i > 0 && pattern[i-1] != '/' || i+2 < len(pattern) && pattern[i+2] != '/' {
					return nil, false
				}
				i++
				if i+1 < len(pattern) {
					b.WriteString("(?:.*/)?")
					i++
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return re, err == nil
}

func regressionStrings(value any) ([]string, bool) {
	array, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(array))
	for _, item := range array {
		word, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, word)
	}
	return out, true
}

// Literal modules are parsed completely. Looking only for an include property
// would silently accept imports, spreads, computed values or later mutations.
func regressionLiteral(text string) (map[string]any, bool) {
	text = strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(text, "export default"); ok {
		text = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(text, "module.exports"); ok {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "=") {
			return nil, false
		}
		text = strings.TrimSpace(rest[1:])
	}
	p := &regressionLiteralParser{text: strings.TrimSpace(strings.TrimSuffix(text, ";"))}
	value, ok := p.value()
	object, objectOK := value.(map[string]any)
	return object, ok && objectOK && strings.TrimSpace(p.text) == ""
}

type regressionLiteralParser struct {
	text  string
	depth int
}

func (p *regressionLiteralParser) take(token string) bool {
	p.text = strings.TrimSpace(p.text)
	if !strings.HasPrefix(p.text, token) {
		return false
	}
	p.text = p.text[len(token):]
	return true
}

func (p *regressionLiteralParser) value() (any, bool) {
	p.depth++
	defer func() { p.depth-- }()
	p.text = strings.TrimSpace(p.text)
	if p.depth > 32 || len(p.text) == 0 {
		return nil, false
	}
	if p.take("{") {
		object := map[string]any{}
		for !p.take("}") {
			key, ok := p.key()
			if _, exists := object[key]; !ok || exists || !p.take(":") {
				return nil, false
			}
			value, ok := p.value()
			if !ok {
				return nil, false
			}
			object[key] = value
			if !p.take(",") {
				return object, p.take("}")
			}
		}
		return object, true
	}
	if p.take("[") {
		array := []any{}
		for !p.take("]") {
			value, ok := p.value()
			if !ok {
				return nil, false
			}
			array = append(array, value)
			if !p.take(",") {
				return array, p.take("]")
			}
		}
		return array, true
	}
	if p.text[0] == '\'' || p.text[0] == '"' {
		return p.quoted()
	}
	for _, literal := range []string{"true", "false", "null"} {
		if p.take(literal) {
			switch literal {
			case "true":
				return true, true
			case "false":
				return false, true
			default:
				return nil, true
			}
		}
	}
	n := 0
	for n < len(p.text) && strings.ContainsRune("0123456789.-+eE", rune(p.text[n])) {
		n++
	}
	if n == 0 {
		return nil, false
	}
	var number float64
	err := json.Unmarshal([]byte(p.text[:n]), &number)
	p.text = p.text[n:]
	return number, err == nil
}

func (p *regressionLiteralParser) key() (string, bool) {
	p.text = strings.TrimSpace(p.text)
	if len(p.text) == 0 {
		return "", false
	}
	if p.text[0] == '\'' || p.text[0] == '"' {
		return p.quoted()
	}
	n := 0
	for n < len(p.text) && (p.text[n] >= 'a' && p.text[n] <= 'z' || p.text[n] >= 'A' && p.text[n] <= 'Z' || p.text[n] == '_') {
		n++
	}
	key := p.text[:n]
	p.text = p.text[n:]
	return key, n > 0
}

func (p *regressionLiteralParser) quoted() (string, bool) {
	quote := p.text[0]
	for i := 1; i < len(p.text); i++ {
		if p.text[i] == '\\' {
			i++
			continue
		}
		if p.text[i] != quote {
			continue
		}
		text := p.text[:i+1]
		p.text = p.text[i+1:]
		if quote == '\'' {
			// Simple single-quoted literals suffice for path declarations.
			text = text[1 : len(text)-1]
			return text, !strings.ContainsAny(text, "\\\n\r")
		}
		value, err := strconv.Unquote(text)
		return value, err == nil
	}
	return "", false
}
