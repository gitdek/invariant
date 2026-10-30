package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const PythonDepsPython = "/opt/invariant/venv/bin/python"

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReportPath = "/opt/invariant/python-report.json"

// Only canonical inputs enter this context. Neither Python's module search nor
// pip's requirement-file parser can reach the original application during build.
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY canonical/ /opt/invariant/canonical/
WORKDIR /opt/invariant/build
ENV PIP_CONFIG_FILE=/dev/null
RUN /usr/local/bin/python -I -m venv /opt/invariant/venv
ARG INVARIANT_PYTHON_RECIPE
LABEL org.invariant.python.recipe=$INVARIANT_PYTHON_RECIPE
RUN /opt/invariant/venv/bin/python -I -m pip --isolated --disable-pip-version-check install --ignore-installed --no-cache-dir --only-binary=:all: --index-url https://pypi.org/simple --report /opt/invariant/python-report.json -r /opt/invariant/canonical/requirements.txt -c /opt/invariant/canonical/constraints.txt
ENV PATH="/opt/invariant/venv/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin"
`

// PythonDependencies describes the application environment, independently of
// the verifier environment. Image is a local execution handle, not evidence.
type PythonDependencies struct {
	Image           string                  `json:"-"`
	Base            string                  `json:"base"`
	Recipe          string                  `json:"recipe"`
	Platform        string                  `json:"platform"`
	Inputs          []PythonDependencyInput `json:"inputs"`
	CanonicalInputs []PythonDependencyInput `json:"canonical_inputs"`
	Artifacts       []PythonArtifact        `json:"artifacts"`
}

type PythonDependencyInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type PythonArtifact struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

type pythonDepsFile struct {
	Path       string `json:"path"`
	Constraint bool   `json:"constraint"`
}

type pythonDepsInclude struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Value      string `json:"value"`
	Constraint bool   `json:"constraint"`
}

type pythonDepsOption struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type pythonDepsRequirement struct {
	Source     string              `json:"source"`
	Line       int                 `json:"line"`
	Name       string              `json:"name"`
	Extras     []string            `json:"extras"`
	Specifier  string              `json:"specifier"`
	Marker     string              `json:"marker"`
	Hashes     map[string][]string `json:"hashes"`
	Constraint bool                `json:"constraint"`
	URL        string              `json:"url"`
	Link       string              `json:"link"`
	Path       string              `json:"path"`
	Archive    bool                `json:"archive"`
	Editable   bool                `json:"editable"`
	Options    []pythonDepsOption  `json:"options"`
}

type pythonDepsParseReport struct {
	Schema       int                     `json:"schema"`
	PipVersion   string                  `json:"pip_version"`
	Root         string                  `json:"root"`
	Files        []pythonDepsFile        `json:"files"`
	Includes     []pythonDepsInclude     `json:"includes"`
	Requirements []pythonDepsRequirement `json:"requirements"`
	Options      []pythonDepsOption      `json:"options"`
	Errors       []struct {
		Source  string `json:"source"`
		Line    int    `json:"line"`
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

type pythonDepsSource struct {
	File pythonDepsFile
	Line int
}

type pythonDepsInputs struct {
	root         string
	original     map[string][]byte
	files        map[pythonDepsFile]bool
	includes     map[pythonDepsInclude]int
	requirements map[pythonDepsSource]int
	canonical    map[string][]byte
}

var (
	pythonDepsDigest           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonDepsName             = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonDepsNamePrefix       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
	pythonDepsCoding           = regexp.MustCompile(`coding[ \t]*[:=]`)
	pythonDepsOptionLiteral    = regexp.MustCompile(`(^|[ \t])--?[A-Za-z]`)
	pythonDepsVersion          = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*(?:[-_.]?(?:a|b|rc|alpha|beta|pre|preview)[-_.]?[0-9]*)?(?:(?:-[0-9]+)|(?:[-_.]?(?:post|rev|r)[-_.]?[0-9]*))?(?:[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)
	pythonDepsWildcard         = regexp.MustCompile(`^(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*\.\*$`)
	pythonDepsArbitraryVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+!-]*$`)
)

func pythonDepsHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// PythonDepsRecipe parses with the pinned base, but never installs packages.
// An absent requirements.txt retains the standard-library execution path.
func PythonDepsRecipe(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inputs, err := readPythonDeps(ctx, root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.environment().Recipe, nil
}

// PythonDepsImage selects one immutable image for both evidence and execution.
// Parsing precedes even a cache lookup; cached evidence cannot bypass policy.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := readPythonDeps(ctx, root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.environment()
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	out, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if inspectErr == nil {
		env.Image = strings.TrimSpace(string(out))
	} else {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		if err := os.Mkdir(filepath.Join(dir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		for name, body := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(dir, "canonical", name), body, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		// Keep the ID file out of the installation context. Build output can
		// contain progress messages; the ID file is Docker's explicit result.
		idFile, err := os.CreateTemp("", "invariant-python-image-")
		if err != nil {
			return nil, err
		}
		idPath := idFile.Name()
		idFile.Close()
		os.Remove(idPath)
		defer os.Remove(idPath)
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", idPath,
			"--build-arg", "INVARIANT_PYTHON_RECIPE="+env.Recipe, "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies: %w\n%s", err, out)
		}
		id, err := os.ReadFile(idPath)
		if err != nil {
			return nil, fmt.Errorf("reading built Python dependency image ID: %w", err)
		}
		env.Image = strings.TrimSpace(string(id))
	}
	if !pythonDepsDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("Python dependency image has no immutable ID: %q", env.Image)
	}
	out, err = Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", env.Image, "/bin/cat", pythonDepsReportPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation evidence: %w\n%s", err, out)
	}
	env.Artifacts, err = pythonDepsArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("Python dependency installation evidence: %w", err)
	}
	return env, nil
}

func (in *pythonDepsInputs) environment() *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform}
	h := sha256.New()
	write := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	for _, value := range []string{pythonDepsDockerfile, pythonDepsParser, PythonImage, pythonDepsPlatform} {
		write([]byte(value))
	}
	for _, group := range []struct {
		name     string
		files    map[string][]byte
		evidence *[]PythonDependencyInput
	}{{"original", in.original, &env.Inputs}, {"canonical", in.canonical, &env.CanonicalInputs}} {
		write([]byte(group.name))
		var names []string
		for name := range group.files {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			body := group.files[name]
			write([]byte(name))
			write(body)
			*group.evidence = append(*group.evidence, PythonDependencyInput{Path: name, SHA256: pythonDepsHash(body)})
		}
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

func readPythonDeps(ctx context.Context, root string) (*pythonDepsInputs, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	in := &pythonDepsInputs{root: root, original: map[string][]byte{}, files: map[pythonDepsFile]bool{},
		includes: map[pythonDepsInclude]int{}, requirements: map[pythonDepsSource]int{}}
	if err := in.visit("requirements.txt", false, map[string]bool{}); err != nil {
		return nil, fmt.Errorf("Python dependencies: %w", err)
	}
	// A Docker bind argument has its own syntax; never reinterpret a project
	// path as an additional mount or access option.
	if strings.ContainsAny(root, ":,\n\r") {
		return nil, fmt.Errorf("Python dependencies: unsupported project mount path %q", root)
	}
	cmd := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", "-v", root+":/project:ro", "-w", "/tmp", PythonImage,
		"/usr/local/bin/python", "-I", "-c", pythonDepsParser, "/project/requirements.txt")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parsing Python dependencies with pinned pip: %w\n%s", err, stderr.String())
	}
	var report pythonDepsParseReport
	if err := pythonDepsDecodeReport(out, &report); err != nil {
		return nil, fmt.Errorf("pinned pip parser report: %w", err)
	}
	if err := in.accept(report); err != nil {
		return nil, fmt.Errorf("pinned pip parser report: %w", err)
	}
	// Bind the report to the same originals used by preflight and hashing.
	for name, before := range in.original {
		resolved, err := in.resolve("", name)
		if err != nil {
			return nil, err
		}
		after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(resolved)))
		if err != nil || !bytes.Equal(before, after) {
			return nil, fmt.Errorf("Python dependency input changed during parsing: %s", name)
		}
	}
	return in, nil
}

// resolve checks each component before cleaning '..', including directories
// that disappear from the final name. Symlinks never enter the input closure.
func (in *pythonDepsInputs) resolve(source, value string) (string, error) {
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, "\\:$\x00\r\n") {
		return "", fmt.Errorf("unsupported local include %q in %s", value, source)
	}
	parts := []string{}
	if source != "" && path.Dir(source) != "." {
		parts = strings.Split(path.Dir(source), "/")
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("include %q escapes the project", value)
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, component)
		}
		info, err := os.Lstat(filepath.Join(in.root, filepath.FromSlash(strings.Join(parts, "/"))))
		if err != nil {
			return "", fmt.Errorf("reading include %q in %s: %w", value, source, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("include %q traverses a symlink", value)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return "", fmt.Errorf("include %q is not a regular file", value)
		}
	}
	name := strings.Join(parts, "/")
	// Opening the literal path also checks that non-final components are
	// directories, even when followed by '..'.
	base := filepath.Join(in.root, filepath.FromSlash(path.Dir(source)))
	literal := base + string(os.PathSeparator) + filepath.FromSlash(value)
	info, err := os.Stat(literal)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("include %q in %s must name a regular file: %v", value, source, err)
	}
	return name, nil
}

func (in *pythonDepsInputs) visit(name string, constraint bool, active map[string]bool) error {
	if active[name] {
		return fmt.Errorf("requirement include cycle at %s", name)
	}
	if _, err := in.resolve("", name); err != nil {
		return err
	}
	active[name] = true
	defer delete(active, name)
	b, err := os.ReadFile(filepath.Join(in.root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	in.original[name] = b
	file := pythonDepsFile{name, constraint}
	in.files[file] = true
	lines, err := pythonDepsLines(b)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for _, line := range lines {
		value, isConstraint, include, err := pythonDepsReference(line.text)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", name, line.number, err)
		}
		if include {
			target, err := in.resolve(name, value)
			if err != nil {
				return err
			}
			in.includes[pythonDepsInclude{name, target, value, isConstraint}]++
			if err := in.visit(target, isConstraint, active); err != nil {
				return err
			}
		} else {
			if err := pythonDepsValidateRequirement(line.text); err != nil {
				return fmt.Errorf("%s:%d: %w", name, line.number, err)
			}
			in.requirements[pythonDepsSource{file, line.number}]++
		}
	}
	return nil
}

type pythonDepsLine struct {
	number int
	text   string
}

func pythonDepsLines(b []byte) ([]pythonDepsLine, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	for i, r := range text {
		if strings.ContainsRune("\x00\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff", r) || (r == '\r' && (i+1 == len(text) || text[i+1] != '\n')) {
			return nil, fmt.Errorf("unsupported requirement separator U+%04X", r)
		}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	physical := strings.Split(text, "\n")
	var lines []pythonDepsLine
	var logical string
	first, continued := 1, false
	for i, line := range physical {
		if !continued {
			first = i + 1
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 && pythonDepsCoding.MatchString(line[comment:]) {
			return nil, fmt.Errorf("encoding declarations in comments are unsupported")
		}
		count := strings.Count(line, "\\")
		if count != 0 {
			if count != 1 || !strings.HasSuffix(line, "\\") || i+1 >= len(physical) || (i+2 == len(physical) && physical[i+1] == "") {
				return nil, fmt.Errorf("only a single trailing continuation backslash is supported")
			}
			logical += strings.TrimSuffix(line, "\\")
			continued = true
			continue
		}
		logical += line
		clean, err := pythonDepsComment(logical)
		if err != nil {
			return nil, err
		}
		if clean != "" {
			lines = append(lines, pythonDepsLine{first, clean})
		}
		logical, continued = "", false
	}
	return lines, nil
}

func pythonDepsComment(line string) (string, error) {
	var quote rune
	for i, r := range line {
		if r == '#' {
			if quote != 0 {
				return "", fmt.Errorf("comment characters in marker literals are unsupported")
			}
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return strings.TrimSpace(line[:i]), nil
			}
		}
		if quote == r {
			quote = 0
		} else if quote == 0 && (r == '\'' || r == '"') {
			quote = r
		}
	}
	return strings.TrimSpace(line), nil
}

func pythonDepsReference(line string) (value string, constraint, include bool, err error) {
	for _, flag := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, flag) {
			continue
		}
		value = strings.TrimPrefix(line, flag)
		constraint, include = flag == "-c" || flag == "--constraint", true
		if strings.HasPrefix(flag, "--") {
			if strings.HasPrefix(value, "=") {
				value = value[1:]
			} else if strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") {
				value = strings.TrimLeft(value, " \t")
			} else {
				return "", false, true, fmt.Errorf("malformed include %q", line)
			}
		} else {
			value = strings.TrimLeft(value, " \t")
		}
		if value == "" || strings.ContainsAny(value, "\"'#$@;=\\:") || strings.HasPrefix(value, "-") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || r < 32 }) >= 0 {
			return "", false, true, fmt.Errorf("unsupported include %q", line)
		}
		return value, constraint, include, nil
	}
	return "", false, false, nil
}

// This grammar is a strict preflight, not an installation parser. It rejects
// unsupported syntax before Docker; only pip's later report is serialized.
func pythonDepsValidateRequirement(line string) error {
	bad := func() error { return fmt.Errorf("unsupported named PyPI requirement %q", line) }
	if !utf8.ValidString(line) || strings.ContainsAny(line, "\r\n\\#\x00$\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff") {
		return bad()
	}
	requirement := line
	if index := strings.Index(line, " --"); index >= 0 {
		start := index
		for start > 0 && line[start-1] == ' ' {
			start--
		}
		if start > 0 {
			r, _ := utf8.DecodeLastRuneInString(line[:start])
			if unicode.IsSpace(r) || r < 32 {
				return bad()
			}
		}
		requirement = line[:start]
		for _, token := range strings.Split(line[index+1:], " ") {
			if token != "" && (!strings.HasPrefix(token, "--hash=") || !pythonDepsDigest.MatchString(strings.TrimPrefix(token, "--hash="))) {
				return bad()
			}
		}
	}
	head, marker, hasMarker := strings.Cut(requirement, ";")
	head = strings.TrimSpace(head)
	name := pythonDepsNamePrefix.FindString(head)
	if !pythonDepsName.MatchString(name) {
		return bad()
	}
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz", ".tbz2"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return bad()
		}
	}
	rest := strings.TrimSpace(head[len(name):])
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return bad()
		}
		for _, extra := range strings.Split(rest[1:end], ",") {
			if !pythonDepsName.MatchString(strings.TrimSpace(extra)) {
				return bad()
			}
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		rest = strings.TrimSpace(rest[1 : len(rest)-1])
		if rest == "" {
			return bad()
		}
	}
	if rest != "" {
		for _, specifier := range strings.Split(rest, ",") {
			specifier = strings.TrimSpace(specifier)
			op := ""
			for _, candidate := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
				if strings.HasPrefix(specifier, candidate) {
					op = candidate
					break
				}
			}
			version := strings.TrimSpace(strings.TrimPrefix(specifier, op))
			valid := pythonDepsVersion.MatchString(version)
			if op == "==" || op == "!=" {
				valid = valid || pythonDepsWildcard.MatchString(version)
			} else if op == "===" {
				valid = pythonDepsArbitraryVersion.MatchString(version)
			} else if strings.Contains(version, "+") {
				valid = false
			}
			if op == "" || !valid || (op == "~=" && !strings.Contains(version, ".")) {
				return bad()
			}
		}
	}
	if hasMarker && !pythonDepsValidMarker(strings.TrimSpace(marker)) {
		return bad()
	}
	return nil
}

type pythonDepsMarker struct {
	tokens []string
	i      int
}

func pythonDepsValidMarker(marker string) bool {
	var tokens []string
	for i := 0; i < len(marker); {
		c := marker[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '\'' || c == '"' {
			end := strings.IndexByte(marker[i+1:], c)
			if end < 0 {
				return false
			}
			end += i + 1
			value := marker[i+1 : end]
			if strings.ContainsAny(value, "#\\\r\n\x00") || pythonDepsOptionLiteral.MatchString(value) {
				return false
			}
			tokens = append(tokens, marker[i:end+1])
			i = end + 1
			continue
		}
		if c == '(' || c == ')' {
			tokens = append(tokens, string(c))
			i++
			continue
		}
		end := i
		for end < len(marker) && ((marker[end] >= 'a' && marker[end] <= 'z') || marker[end] == '_') {
			end++
		}
		if end > i {
			tokens = append(tokens, marker[i:end])
			i = end
			continue
		}
		found := false
		for _, op := range []string{"===", "==", "!=", "<=", ">=", "~=", "<", ">"} {
			if strings.HasPrefix(marker[i:], op) {
				tokens = append(tokens, op)
				i += len(op)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	p := pythonDepsMarker{tokens: tokens}
	return p.expression() && p.i == len(tokens)
}

func (p *pythonDepsMarker) take(token string) bool {
	if p.i < len(p.tokens) && p.tokens[p.i] == token {
		p.i++
		return true
	}
	return false
}

func (p *pythonDepsMarker) operand() bool {
	if p.i == len(p.tokens) {
		return false
	}
	token := p.tokens[p.i]
	if token[0] == '\'' || token[0] == '"' {
		p.i++
		return true
	}
	switch token {
	case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra":
		p.i++
		return true
	}
	return false
}

func (p *pythonDepsMarker) atom() bool {
	if p.take("(") {
		return p.expression() && p.take(")")
	}
	if !p.operand() || p.i == len(p.tokens) {
		return false
	}
	if p.take("not") {
		if !p.take("in") {
			return false
		}
	} else {
		switch p.tokens[p.i] {
		case "in", "==", "!=", "<=", ">=", "<", ">", "~=", "===":
			p.i++
		default:
			return false
		}
	}
	return p.operand()
}

func (p *pythonDepsMarker) expression() bool {
	if !p.atom() {
		return false
	}
	for p.take("and") || p.take("or") {
		if !p.atom() {
			return false
		}
	}
	return true
}

func pythonDepsDecodeReport(b []byte, report *pythonDepsParseReport) error {
	if !json.Valid(b) {
		return fmt.Errorf("missing or malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(report); err != nil {
		return err
	}
	// Zero values alone cannot distinguish a complete report from a truncated
	// schema that happened to remain valid JSON. Require every field, even
	// empty arrays and false source-form flags, at this versioned boundary.
	var complete func(json.RawMessage, reflect.Type) error
	complete = func(raw json.RawMessage, typ reflect.Type) error {
		if len(raw) == 0 || string(raw) == "null" {
			return fmt.Errorf("missing parser field (%s)", typ)
		}
		switch typ.Kind() {
		case reflect.Struct:
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				return err
			}
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				name := field.Tag.Get("json")
				if err := complete(object[name], field.Type); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
		case reflect.Slice:
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return err
			}
			for _, item := range items {
				if err := complete(item, typ.Elem()); err != nil {
					return err
				}
			}
		case reflect.Map:
			var items map[string]json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return err
			}
			for _, item := range items {
				if err := complete(item, typ.Elem()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return complete(b, reflect.TypeOf(*report))
}

func (in *pythonDepsInputs) accept(report pythonDepsParseReport) error {
	if report.Schema != 1 || report.PipVersion == "" || report.Root != "requirements.txt" {
		return fmt.Errorf("unsupported or incomplete parser identity")
	}
	if len(report.Errors) != 0 {
		err := report.Errors[0]
		return fmt.Errorf("%s:%d: %s: %s", err.Source, err.Line, err.Type, err.Message)
	}
	if len(report.Options) != 0 {
		option := report.Options[0]
		return fmt.Errorf("%s:%d: pip option %s is unsupported", option.Source, option.Line, option.Name)
	}
	files := map[pythonDepsFile]bool{}
	for _, file := range report.Files {
		if !in.files[file] || files[file] {
			return fmt.Errorf("unexpected followed file or role: %s (constraint=%t)", file.Path, file.Constraint)
		}
		files[file] = true
	}
	if !reflect.DeepEqual(files, in.files) {
		return fmt.Errorf("incomplete followed-file report")
	}
	includes := map[pythonDepsInclude]bool{}
	for _, include := range report.Includes {
		if in.includes[include] == 0 {
			return fmt.Errorf("unexpected include from %s: %q", include.Source, include.Value)
		}
		target, err := in.resolve(include.Source, include.Value)
		if err != nil || target != include.Target || !files[pythonDepsFile{include.Target, include.Constraint}] {
			return fmt.Errorf("unsafe or incomplete include report from %s: %q", include.Source, include.Value)
		}
		includes[include] = true
	}
	if len(includes) != len(in.includes) {
		return fmt.Errorf("incomplete include report")
	}
	counts := map[pythonDepsSource]int{}
	canonical := map[string][]byte{"requirements.txt": {}, "constraints.txt": {}}
	for _, requirement := range report.Requirements {
		source := pythonDepsSource{pythonDepsFile{requirement.Source, requirement.Constraint}, requirement.Line}
		if in.requirements[source] == 0 || !files[source.File] {
			return fmt.Errorf("requirement from unreported file, line or role: %s:%d", requirement.Source, requirement.Line)
		}
		counts[source]++
		if requirement.URL != "" || requirement.Link != "" || requirement.Path != "" || requirement.Archive || requirement.Editable || len(requirement.Options) != 0 {
			return fmt.Errorf("%s:%d: only named PyPI requirements with hashes are supported", requirement.Source, requirement.Line)
		}
		if !pythonDepsName.MatchString(requirement.Name) {
			return fmt.Errorf("invalid reported requirement name %q", requirement.Name)
		}
		line := requirement.Name
		extras := append([]string(nil), requirement.Extras...)
		sort.Strings(extras)
		for _, extra := range extras {
			if !pythonDepsName.MatchString(extra) {
				return fmt.Errorf("invalid reported requirement extra %q", extra)
			}
		}
		if len(extras) != 0 {
			line += "[" + strings.Join(extras, ",") + "]"
		}
		// Serialize semantic fields separately: a specifier cannot smuggle
		// a marker or option, nor can a marker smuggle a hash option.
		if strings.ContainsAny(requirement.Specifier, ";#\\\r\n[]") || strings.Contains(requirement.Specifier, "--") {
			return fmt.Errorf("invalid reported specifier %q", requirement.Specifier)
		}
		line += requirement.Specifier
		if requirement.Marker != "" {
			if !pythonDepsValidMarker(requirement.Marker) {
				return fmt.Errorf("invalid reported marker %q", requirement.Marker)
			}
			line += "; " + requirement.Marker
		}
		for algorithm, values := range requirement.Hashes {
			if algorithm != "sha256" || len(values) == 0 {
				return fmt.Errorf("unsupported reported hash algorithm %q", algorithm)
			}
			hashes := append([]string(nil), values...)
			sort.Strings(hashes)
			for _, hash := range hashes {
				if !pythonDepsDigest.MatchString("sha256:" + hash) {
					return fmt.Errorf("invalid reported SHA-256 hash")
				}
				line += " --hash=sha256:" + hash
			}
		}
		if err := pythonDepsValidateRequirement(line); err != nil {
			return err
		}
		filename := "requirements.txt"
		if requirement.Constraint {
			filename = "constraints.txt"
		}
		canonical[filename] = append(canonical[filename], []byte(line+"\n")...)
	}
	if !reflect.DeepEqual(counts, in.requirements) {
		return fmt.Errorf("incomplete requirement report")
	}
	in.canonical = canonical
	return nil
}

// Pip's installation report records the downloaded wheels, including
// transitives. An empty install array is valid; an absent one is not.
func pythonDepsArtifacts(b []byte) ([]PythonArtifact, error) {
	var report struct {
		Version    string `json:"version"`
		PipVersion string `json:"pip_version"`
		Install    []struct {
			Download struct {
				URL     string `json:"url"`
				Archive struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, fmt.Errorf("malformed pip installation report: %w", err)
	}
	if report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("incomplete pip installation report")
	}
	artifacts := make([]PythonArtifact, 0, len(report.Install))
	seen := map[string]bool{}
	for _, item := range report.Install {
		u, err := url.Parse(item.Download.URL)
		if err != nil || u.Scheme == "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("invalid resolved wheel URL %q", item.Download.URL)
		}
		filename := path.Base(u.Path)
		hash := "sha256:" + item.Download.Archive.Hashes["sha256"]
		if !pythonDepsName.MatchString(item.Metadata.Name) || item.Metadata.Version == "" || !strings.HasSuffix(filename, ".whl") || strings.ContainsAny(filename, "\\\r\n") || !pythonDepsDigest.MatchString(hash) {
			return nil, fmt.Errorf("incomplete resolved wheel evidence for %q", item.Metadata.Name)
		}
		name := strings.ToLower(item.Metadata.Name)
		name = regexp.MustCompile(`[-_.]+`).ReplaceAllString(name, "-")
		if seen[name] {
			return nil, fmt.Errorf("duplicate resolved distribution %q", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonArtifact{Name: name, Version: item.Metadata.Version, Filename: filename, SHA256: hash})
	}
	sort.Slice(artifacts, func(i, j int) bool {
		a, b := artifacts[i], artifacts[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return a.Filename < b.Filename
	})
	return artifacts, nil
}

// pythonDepsParser runs only in the pinned Python base, isolated from the
// application's import path. Pip owns both requirement-file syntax and the
// interpretation of each install requirement; this helper observes its result.
const pythonDepsParser = `
import contextlib
import json
import optparse
import os
import sys
import pip
from pip._internal.exceptions import InstallationError, PipError
from pip._internal.network.session import PipSession
from pip._internal.req.req_file import RequirementsFileParser, parse_requirements
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._internal.utils.filetypes import is_archive_file
from pip._vendor.packaging.utils import canonicalize_name

root = os.path.abspath(sys.argv[1])
project = os.path.dirname(root)
report = {"schema": 1, "pip_version": pip.__version__, "root": "requirements.txt",
          "files": [], "includes": [], "requirements": [], "options": [], "errors": []}
files = {}
includes = {}
active = set()
origins = {}
events = []
current = {"source": "requirements.txt", "line": 1, "options": []}

def relative(filename):
    return os.path.relpath(os.path.abspath(filename), project).replace(os.sep, "/")

def checked(filename):
    if "://" in filename:
        raise InstallationError("requirements includes must be local project files")
    absolute = os.path.abspath(filename)
    if os.path.commonpath((project, absolute)) != project or not filename.startswith(project + os.sep):
        raise InstallationError("requirements include leaves the project")
    # Check literal components before collapsing '..': a symlink followed by
    # '..' is still followed by the filesystem when pip opens the original path.
    cursor = project
    for part in filename[len(project) + 1:].split(os.sep):
        if part in ("", "."):
            continue
        if part == "..":
            if cursor == project:
                raise InstallationError("requirements include leaves the project")
            cursor = os.path.dirname(cursor)
            continue
        cursor = os.path.join(cursor, part)
        if os.path.islink(cursor):
            raise InstallationError("requirements include follows a symlink")
    return absolute

def no_network(*args, **kwargs):
    raise InstallationError("network access is forbidden while parsing requirements")

# Observe explicit options as pip consumes them. Looking only at yielded
# requirements loses standalone option lines; inspecting Values alone loses
# the distinction between an explicit option and its default value.
take_action = optparse.Option.take_action
def observe_option(self, action, dest, opt, value, values, parser):
    result = take_action(self, action, dest, opt, value, values, parser)
    name = self.get_opt_string()
    events.append({"name": name, "value": "" if value is None else str(value)})
    return result
optparse.Option.take_action = observe_option

parse_file = RequirementsFileParser._parse_file
def observe_file(self, filename, constraint):
    global events, current
    source = relative(filename) if "://" not in filename else filename
    origin = origins.get(os.path.abspath(filename), (source, 1))
    current = {"source": origin[0], "line": origin[1], "options": []}
    absolute = checked(filename)
    if os.path.isfile(absolute):
        # Missing includes belong to the including row; syntax errors in an
        # existing nested file belong to that file even before its first yield.
        current = {"source": source, "line": 1, "options": []}
    if absolute in active:
        raise InstallationError("cyclic requirements include: " + source)
    active.add(absolute)
    file_row = {"path": source, "constraint": bool(constraint)}
    files[json.dumps(file_row, sort_keys=True)] = file_row
    try:
        iterator = iter(parse_file(self, filename, constraint))
        while True:
            events = []
            try:
                line = next(iterator)
            except StopIteration:
                break
            represented = {"--hash"} if line.is_requirement else {"--requirement", "--constraint"}
            options = [{"source": source, "line": line.lineno, **event}
                       for event in events if event["name"] not in represented]
            current = {"source": source, "line": line.lineno, "options": options}
            if not line.is_requirement:
                report["options"].extend(options)
                for values, role in ((line.opts.requirements, False), (line.opts.constraints, True)):
                    for value in values or []:
                        if not value or os.path.isabs(value) or "://" in value:
                            raise InstallationError("requirements include must name a relative project file")
                        target_path = os.path.join(os.path.dirname(filename), value)
                        target = relative(target_path)
                        include = {"source": source, "target": target, "value": value, "constraint": role}
                        includes[json.dumps(include, sort_keys=True)] = include
                        checked(target_path)
                        origins[os.path.abspath(target_path)] = (source, line.lineno)
            yield line
    finally:
        active.remove(absolute)
RequirementsFileParser._parse_file = observe_file

try:
    # Keep the versioned report as the only stdout output even if pip emits a
    # diagnostic while constructing an unsupported local/archive requirement.
    with contextlib.redirect_stdout(sys.stderr), PipSession() as session:
        session.request = no_network
        for parsed in parse_requirements(root, session=session):
            install = install_req_from_parsed_requirement(parsed, isolated=True)
            requirement = install.req
            link = install.link
            report["requirements"].append({
                "source": current["source"], "line": current["line"],
                "name": canonicalize_name(requirement.name) if requirement is not None else "",
                "extras": sorted({canonicalize_name(extra) for extra in requirement.extras}) if requirement is not None else [],
                "specifier": str(requirement.specifier) if requirement is not None else "",
                "marker": str(install.markers) if install.markers else "",
                "hashes": {algorithm: sorted(set(values)) for algorithm, values in install.hash_options.items()},
                "constraint": bool(install.constraint),
                "url": (requirement.url or "") if requirement is not None else "",
                "link": str(link) if link else "",
                "path": link.file_path if link and link.is_file else "",
                "archive": bool(link and is_archive_file(link.filename)),
                "editable": bool(install.editable),
                "options": current["options"],
            })
except (PipError, UnicodeError) as error:
    report["errors"].append({"source": current["source"], "line": current["line"],
                             "type": type(error).__name__, "message": str(error)})

report["files"] = [files[key] for key in sorted(files)]
report["includes"] = [includes[key] for key in sorted(includes)]
print(json.dumps(report, sort_keys=True))
`
