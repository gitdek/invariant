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

// PythonLibraryDir is deliberately neither a site directory nor on PATH.
// The gate adds it to the import path only after isolated interpreter startup.
const PythonLibraryDir = "/opt/invariant-libraries"

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant-evidence/install.json"

// Only generated files enter this image. pip never opens an application input
// during installation, and installed scripts cannot replace the base tools.
const pythonDepsDockerfile = "FROM --platform=linux/amd64 " + PythonImage + `
COPY canonical/ /opt/invariant-inputs/
WORKDIR /tmp
RUN /bin/mkdir -p /opt/invariant-evidence
RUN /usr/local/bin/python -I -m pip --isolated --disable-pip-version-check install --no-cache-dir --only-binary=:all: --index-url https://pypi.org/simple --target ` + PythonLibraryDir + ` --report ` + pythonDepsReport + ` --requirement /opt/invariant-inputs/requirements.txt --constraint /opt/invariant-inputs/constraints.txt
`

// PythonDependencies records both input representations and the actual wheel
// resolution saved in an image. Image selects execution, not portable evidence.
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

type pythonInputFile struct {
	Path       string `json:"path"`
	Constraint bool   `json:"constraint"`
}

type pythonInclude struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Value      string `json:"value"`
	Constraint bool   `json:"constraint"`
}

type pythonPipOption struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type pythonPipRequirement struct {
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
	Options    []pythonPipOption   `json:"options"`
}

type pythonPipError struct {
	Source  string `json:"source"`
	Line    int    `json:"line"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type pythonPipReport struct {
	Schema       int                    `json:"schema"`
	PipVersion   string                 `json:"pip_version"`
	Root         string                 `json:"root"`
	Files        []pythonInputFile      `json:"files"`
	Includes     []pythonInclude        `json:"includes"`
	Requirements []pythonPipRequirement `json:"requirements"`
	Options      []pythonPipOption      `json:"options"`
	Errors       []pythonPipError       `json:"errors"`
}

type pythonInputs struct {
	root         string
	bytes        map[string][]byte
	files        map[pythonInputFile]bool
	includes     map[pythonInclude]bool
	requirements map[pythonInputFile]int
	canonical    map[string][]byte
}

// PythonDepsRecipe performs the same preflight and pinned-pip parse as a build,
// without selecting or constructing any application image.
func PythonDepsRecipe(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inputs, err := pythonDependencyInputs(ctx, root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.evidence().Recipe, nil
}

// PythonDepsImage never falls back to the base on a failed parse, installation
// or evidence read. The mutable recipe tag is resolved exactly once; all later
// reads and checks use that immutable image ID, even if another build retags it.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := pythonDependencyInputs(ctx, root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.evidence()
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	id, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if inspectErr != nil {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		buildDir := filepath.Join(dir, "context")
		if err := os.MkdirAll(filepath.Join(buildDir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		for name, b := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(buildDir, "canonical", name), b, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		iidfile := filepath.Join(dir, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, buildDir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies: %w\n%s", err, out)
		}
		id, err = os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("reading Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(id))
	if !pythonDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("invalid immutable Python dependency image ID %q", env.Image)
	}
	cmd := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", "-w", "/tmp", env.Image, "/bin/cat", pythonDepsReport)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	report, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation evidence: %w\n%s", err, diagnostics.String())
	}
	env.Artifacts, err = pythonArtifacts(report)
	if err != nil {
		return nil, err
	}
	return env, nil
}

func pythonSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (p *pythonInputs) evidence() *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform}
	h := sha256.New()
	add := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	for _, s := range []string{pythonDepsDockerfile, pythonPipParser, PythonImage, pythonDepsPlatform} {
		add([]byte(s))
	}
	var paths []string
	for filename := range p.bytes {
		paths = append(paths, filename)
	}
	sort.Strings(paths)
	for _, filename := range paths {
		add([]byte(filename))
		add(p.bytes[filename])
		env.Inputs = append(env.Inputs, PythonDependencyInput{filename, pythonSHA256(p.bytes[filename])})
	}
	for _, filename := range []string{"constraints.txt", "requirements.txt"} {
		add([]byte("canonical/" + filename))
		add(p.canonical[filename])
		env.CanonicalInputs = append(env.CanonicalInputs, PythonDependencyInput{filename, pythonSHA256(p.canonical[filename])})
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

func pythonDependencyInputs(ctx context.Context, root string) (*pythonInputs, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p := &pythonInputs{root: root, bytes: map[string][]byte{}, files: map[pythonInputFile]bool{}, includes: map[pythonInclude]bool{}, requirements: map[pythonInputFile]int{}}
	if err := p.visit("requirements.txt", false, map[string]bool{}); err != nil {
		return nil, fmt.Errorf("Python dependency preflight: %w", err)
	}
	cmd := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", "--memory", "512m", "--pids-limit", "64", "-v", root+":/project:ro", "-w", "/tmp",
		PythonImage, "/usr/local/bin/python", "-I", "-B", "-c", pythonPipParser)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parsing Python dependencies with pinned pip: %w\n%s", err, diagnostics.String())
	}
	var report pythonPipReport
	if err := pythonReportShape(out); err != nil {
		return nil, fmt.Errorf("incomplete pinned pip dependency report: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return nil, fmt.Errorf("invalid pinned pip dependency report: %w", err)
	}
	if err := p.acceptReport(report); err != nil {
		return nil, fmt.Errorf("pinned pip dependency report: %w", err)
	}
	// Bind the report to the preflight snapshot; do not build after a concurrent
	// edit, even when the changed syntax would still be accepted.
	for include := range p.includes {
		target, err := p.resolve(include.Source, include.Value)
		if err != nil || target != include.Target {
			return nil, fmt.Errorf("Python dependency include changed while pip parsed it: %s in %s", include.Value, include.Source)
		}
	}
	for filename, before := range p.bytes {
		if _, err := p.resolve("requirements.txt", filename); err != nil {
			return nil, err
		}
		after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
		if err != nil || !bytes.Equal(before, after) {
			return nil, fmt.Errorf("Python dependency input changed while pip parsed it: %s", filename)
		}
	}
	return p, nil
}

func (p *pythonInputs) visit(filename string, constraint bool, active map[string]bool) error {
	if active[filename] {
		return fmt.Errorf("requirement include cycle at %s", filename)
	}
	file := pythonInputFile{filename, constraint}
	if p.files[file] {
		return nil
	}
	if _, err := p.resolve("requirements.txt", filename); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(filename)))
	if err != nil {
		return err
	}
	lines, err := pythonLogicalLines(b)
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	p.files[file], p.bytes[filename], active[filename] = true, b, true
	defer delete(active, filename)
	for _, line := range lines {
		value, role, include, err := pythonIncludeValue(line)
		if err != nil {
			return fmt.Errorf("%s: %w", filename, err)
		}
		if include {
			target, err := p.resolve(filename, value)
			if err != nil {
				return err
			}
			p.includes[pythonInclude{filename, target, value, role}] = true
			if err := p.visit(target, role, active); err != nil {
				return err
			}
		} else {
			if err := pythonRequirement(line); err != nil {
				return fmt.Errorf("%s: %w", filename, err)
			}
			p.requirements[file]++
		}
	}
	return nil
}

// Inspect each component before cleaning .. away: a canceled directory can
// still escape the project or traverse a symlink when pip opens the reference.
func (p *pythonInputs) resolve(source, value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\:$\x00\"'") || path.IsAbs(value) || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("unsupported requirement reference %q in %s", value, source)
	}
	current := path.Dir(source)
	parts := strings.Split(value, "/")
	for i, part := range parts {
		current = path.Join(current, part)
		if current == ".." || strings.HasPrefix(current, "../") {
			return "", fmt.Errorf("requirement reference %q escapes the project", value)
		}
		info, err := os.Lstat(filepath.Join(p.root, filepath.FromSlash(current)))
		if err != nil {
			return "", fmt.Errorf("requirement reference %q: %w", value, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("requirement reference %q must traverse directories without symlinks and name a regular file", value)
		}
	}
	return current, nil
}

var (
	pythonDigest     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonName       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonNamePrefix = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
	pythonEncoding   = regexp.MustCompile(`coding[ \t]*[:=]`)
	pythonSpecifier  = regexp.MustCompile(`^(===|~=|==|!=|<=|>=|<|>)[ \t]*([A-Za-z0-9][A-Za-z0-9.!+_*\-]*)$`)
)

func pythonLogicalLines(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	if strings.ContainsAny(s, "\x00\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff") {
		return nil, fmt.Errorf("unsupported requirement line separator or embedded BOM")
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' && (i+1 == len(s) || s[i+1] != '\n') {
			return nil, fmt.Errorf("requirements permit only LF or CRLF endings")
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var result []string
	var logical string
	continuing := false
	physical := strings.Split(s, "\n")
	for i, line := range physical {
		if strings.Contains(line, "\\") {
			if strings.Count(line, "\\") != 1 || !strings.HasSuffix(line, "\\") || strings.Contains(line, "#") || i+1 >= len(physical) || (i+1 == len(physical)-1 && physical[i+1] == "") {
				return nil, fmt.Errorf("only a single trailing continuation backslash is supported")
			}
			logical += strings.TrimSuffix(line, "\\")
			continuing = true
			continue
		}
		logical += line
		clean, err := pythonStripComment(logical)
		if err != nil {
			return nil, err
		}
		if clean = strings.Trim(clean, " \t"); clean != "" {
			result = append(result, clean)
		}
		logical, continuing = "", false
	}
	if continuing {
		return nil, fmt.Errorf("dangling requirement continuation")
	}
	return result, nil
}

func pythonStripComment(line string) (string, error) {
	var quote rune
	for i, r := range line {
		if r == '#' {
			if quote != 0 {
				return "", fmt.Errorf("comment characters in marker literals are unsupported")
			}
			if pythonEncoding.MatchString(line[i:]) {
				return "", fmt.Errorf("encoding declarations in requirements are unsupported")
			}
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i], nil
			}
			return "", fmt.Errorf("unsupported comment boundary")
		}
		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
		}
	}
	return line, nil
}

func pythonIncludeValue(line string) (string, bool, bool, error) {
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		rest := strings.TrimPrefix(line, option)
		if len(option) > 2 {
			if strings.HasPrefix(rest, "=") {
				rest = rest[1:]
				if rest == "" || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
					return "", false, true, fmt.Errorf("empty %s value", option)
				}
			} else if strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
				rest = strings.TrimLeft(rest, " \t")
			} else {
				return "", false, true, fmt.Errorf("invalid include option %q", line)
			}
		} else {
			rest = strings.TrimLeft(rest, " \t")
		}
		if rest == "" || strings.IndexFunc(rest, func(r rune) bool { return unicode.IsSpace(r) || r == '\u001f' }) >= 0 {
			return "", false, true, fmt.Errorf("invalid include value %q", rest)
		}
		return rest, option == "-c" || option == "--constraint", true, nil
	}
	return "", false, false, nil
}

// This deliberately strict preflight does not produce installation inputs.
// It rejects ambiguous syntax before Docker; only pip's report is serialized.
func pythonRequirement(line string) error {
	bad := func() error { return fmt.Errorf("unsupported Python requirement %q", line) }
	if strings.ContainsAny(line, "\\#$\r\n\x00") || !utf8.ValidString(line) {
		return bad()
	}
	// pip breaks arguments from options only at ASCII spaces, even inside
	// quotes. Never repair a tab/Unicode boundary into an accepted hash option.
	var quote rune
	hashStart := -1
	for i, r := range line {
		if r == '\'' || r == '"' {
			if quote == 0 {
				quote = r
			} else if quote == r {
				quote = 0
			}
		}
		if (unicode.IsSpace(r) && r != ' ' && r != '\t') || r == '\u001f' {
			return bad()
		}
		if r == '-' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t' || line[i-1] == '\'' || line[i-1] == '"') {
			if quote != 0 || i == 0 || line[i-1] != ' ' || !strings.HasPrefix(line[i:], "--hash=") {
				return bad()
			}
			hashStart = i
			break
		}
	}
	if hashStart >= 0 {
		for _, token := range strings.Split(line[hashStart:], " ") {
			if token != "" && (!strings.HasPrefix(token, "--hash=") || !pythonDigest.MatchString(strings.TrimPrefix(token, "--hash="))) {
				return bad()
			}
		}
		line = strings.TrimRight(line[:hashStart], " ")
	}
	req, marker, hasMarker := strings.Cut(line, ";")
	if hasMarker && !validPythonMarker(strings.TrimSpace(marker)) {
		return bad()
	}
	req = strings.Trim(req, " \t")
	name := pythonNamePrefix.FindString(req)
	if !pythonName.MatchString(name) || pythonArchiveName(name) {
		return bad()
	}
	rest := strings.Trim(req[len(name):], " \t")
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return bad()
		}
		for _, extra := range strings.Split(rest[1:end], ",") {
			if !pythonName.MatchString(strings.Trim(extra, " \t")) {
				return bad()
			}
		}
		rest = strings.Trim(rest[end+1:], " \t")
	}
	if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		rest = strings.Trim(rest[1:len(rest)-1], " \t")
		if rest == "" {
			return bad()
		}
	}
	if rest != "" {
		for _, spec := range strings.Split(rest, ",") {
			if !pythonSpecifier.MatchString(strings.Trim(spec, " \t")) {
				return bad()
			}
		}
	}
	return nil
}

func pythonArchiveName(name string) bool {
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz", ".tbz2", ".txz", ".tlz", ".tar.lz", ".tar.lzma", ".tar.z", ".taz", ".xz", ".bz2"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return true
		}
	}
	return false
}

// Markers remain unevaluated. A small recursive grammar rejects suffixes and
// option-like quoted atoms before pip sees them; pip supplies final semantics.
type pythonMarkerParser struct{ text string }

func validPythonMarker(s string) bool {
	p := pythonMarkerParser{text: s}
	return p.expression() && strings.TrimSpace(p.text) == ""
}

func (p *pythonMarkerParser) take(token string) bool {
	p.text = strings.TrimLeft(p.text, " \t")
	if !strings.HasPrefix(p.text, token) {
		return false
	}
	if token[0] >= 'a' && token[0] <= 'z' && len(p.text) > len(token) {
		c := p.text[len(token)]
		if (c >= 'a' && c <= 'z') || c == '_' {
			return false
		}
	}
	p.text = p.text[len(token):]
	return true
}

func (p *pythonMarkerParser) expression() bool {
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

func (p *pythonMarkerParser) atom() bool {
	if p.take("(") {
		return p.expression() && p.take(")")
	}
	if !p.value() {
		return false
	}
	op := false
	for _, token := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">", "in"} {
		if p.take(token) {
			op = true
			break
		}
	}
	if !op && p.take("not") {
		op = p.take("in")
	}
	return op && p.value()
}

func (p *pythonMarkerParser) value() bool {
	p.text = strings.TrimLeft(p.text, " \t")
	if p.text == "" {
		return false
	}
	if q := p.text[0]; q == '\'' || q == '"' {
		end := strings.IndexByte(p.text[1:], q)
		if end < 0 {
			return false
		}
		value := p.text[1 : 1+end]
		if strings.ContainsAny(value, "\\#\r\n") {
			return false
		}
		for _, word := range strings.Fields(value) {
			if strings.HasPrefix(word, "-") {
				return false
			}
		}
		p.text = p.text[end+2:]
		return true
	}
	for _, name := range []string{"python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra"} {
		if p.take(name) {
			return true
		}
	}
	return false
}

func (p *pythonInputs) acceptReport(r pythonPipReport) error {
	if r.Schema != 1 || r.PipVersion == "" || r.Root != "requirements.txt" || r.Files == nil || r.Includes == nil || r.Requirements == nil || r.Options == nil || r.Errors == nil {
		return fmt.Errorf("missing or unsupported parser report fields")
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("pip refused %s:%d: %s: %s", r.Errors[0].Source, r.Errors[0].Line, r.Errors[0].Type, r.Errors[0].Message)
	}
	if len(r.Options) > 0 {
		return fmt.Errorf("unsupported pip option %s", r.Options[0].Name)
	}
	files := map[pythonInputFile]bool{}
	for _, file := range r.Files {
		if !p.files[file] {
			return fmt.Errorf("unaccounted input or role %q", file.Path)
		}
		files[file] = true
	}
	if !reflect.DeepEqual(files, p.files) {
		return fmt.Errorf("incomplete followed-file report")
	}
	includes := map[pythonInclude]bool{}
	for _, include := range r.Includes {
		if !p.includes[include] {
			return fmt.Errorf("unaccounted include %q from %s", include.Value, include.Source)
		}
		includes[include] = true
	}
	if !reflect.DeepEqual(includes, p.includes) {
		return fmt.Errorf("incomplete include report")
	}
	p.canonical = map[string][]byte{"requirements.txt": {}, "constraints.txt": {}}
	counts := map[pythonInputFile]int{}
	for _, row := range r.Requirements {
		file := pythonInputFile{row.Source, row.Constraint}
		if !files[file] || row.Line <= 0 || row.Extras == nil || row.Hashes == nil || row.Options == nil || row.URL != "" || row.Link != "" || row.Path != "" || row.Archive || row.Editable || len(row.Options) != 0 || !pythonName.MatchString(row.Name) {
			return fmt.Errorf("unsupported or incomplete requirement from %s:%d", row.Source, row.Line)
		}
		counts[file]++
		line := row.Name
		extras := append([]string(nil), row.Extras...)
		sort.Strings(extras)
		for _, extra := range extras {
			if !pythonName.MatchString(extra) {
				return fmt.Errorf("invalid pip-reported extra %q", extra)
			}
		}
		if len(extras) > 0 {
			line += "[" + strings.Join(extras, ",") + "]"
		}
		line += row.Specifier
		if row.Marker != "" {
			line += "; " + row.Marker
		}
		for algorithm, values := range row.Hashes {
			if algorithm != "sha256" || len(values) == 0 {
				return fmt.Errorf("unsupported pip hash algorithm %q", algorithm)
			}
			values = append([]string(nil), values...)
			sort.Strings(values)
			for _, value := range values {
				if !pythonDigest.MatchString(algorithm + ":" + value) {
					return fmt.Errorf("invalid pip-reported hash")
				}
				line += " --hash=" + algorithm + ":" + value
			}
		}
		if err := pythonRequirement(line); err != nil {
			return err
		}
		filename := "requirements.txt"
		if row.Constraint {
			filename = "constraints.txt"
		}
		p.canonical[filename] = append(p.canonical[filename], []byte(line+"\n")...)
	}
	for file, count := range p.requirements {
		if counts[file] < count {
			return fmt.Errorf("incomplete requirement report for %s", file.Path)
		}
	}
	return nil
}

// Missing booleans or empty strings must not silently become safe zero values.
// Schema 1 requires every observation, including explicit empty collections.
func pythonReportShape(raw []byte) error {
	object := func(raw json.RawMessage, keys string) (map[string]json.RawMessage, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
		for _, key := range strings.Fields(keys) {
			value, ok := fields[key]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, fmt.Errorf("missing %s", key)
			}
		}
		return fields, nil
	}
	fields, err := object(raw, "schema pip_version root files includes requirements options errors")
	if err != nil {
		return err
	}
	for key, keys := range map[string]string{
		"files":        "path constraint",
		"includes":     "source target value constraint",
		"requirements": "source line name extras specifier marker hashes constraint url link path archive editable options",
		"options":      "source line name value",
		"errors":       "source line type message",
	} {
		var rows []json.RawMessage
		if err := json.Unmarshal(fields[key], &rows); err != nil {
			return err
		}
		for _, row := range rows {
			entry, err := object(row, keys)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			if key == "requirements" {
				var options []json.RawMessage
				if err := json.Unmarshal(entry["options"], &options); err != nil {
					return err
				}
				for _, option := range options {
					if _, err := object(option, "source line name value"); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func pythonArtifacts(raw []byte) ([]PythonArtifact, error) {
	var report struct {
		Version    string `json:"version"`
		PipVersion string `json:"pip_version"`
		Install    []struct {
			DownloadInfo struct {
				URL         string `json:"url"`
				ArchiveInfo struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
			Metadata struct{ Name, Version string } `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("malformed Python dependency installation report: %w", err)
	}
	if report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("missing Python dependency installation evidence")
	}
	artifacts := []PythonArtifact{}
	for _, installed := range report.Install {
		u, err := url.Parse(installed.DownloadInfo.URL)
		if err != nil || u.Scheme == "" || u.Path == "" {
			return nil, fmt.Errorf("Python dependency evidence does not name a wheel artifact")
		}
		filename := path.Base(u.Path)
		hash := "sha256:" + installed.DownloadInfo.ArchiveInfo.Hashes["sha256"]
		if !strings.HasSuffix(filename, ".whl") || !pythonName.MatchString(installed.Metadata.Name) || installed.Metadata.Version == "" || !pythonDigest.MatchString(hash) {
			return nil, fmt.Errorf("Python dependency wheel evidence is missing a name, version, filename or SHA-256 hash")
		}
		artifacts = append(artifacts, PythonArtifact{installed.Metadata.Name, installed.Metadata.Version, filename, hash})
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

// pythonPipParser observes the pinned base's own parser and requirement
// constructor. Its report includes options and include-only lines that
// parse_requirements alone does not yield, and never evaluates markers.
// Unexpected exceptions are process failures, not successful input refusals.
const pythonPipParser = `
import json
import optparse
import os
import pip
from pip._internal.exceptions import InstallationError
from pip._internal.network.session import PipSession
from pip._internal.req import req_file
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._internal.utils.filetypes import is_archive_file
from pip._vendor.packaging.utils import canonicalize_name

root = "/project"
report = {
    "schema": 1,
    "pip_version": pip.__version__,
    "root": "requirements.txt",
    "files": [],
    "includes": [],
    "requirements": [],
    "options": [],
    "errors": [],
}
files = {}
includes = {}
file_stack = []
location = {"source": "requirements.txt", "line": 0}
observed_options = []
requirement_options = {}

def relative(filename):
    if "://" in filename:
        return filename
    return os.path.relpath(os.path.normpath(filename), root)

def no_network(*args, **kwargs):
    raise AssertionError("authoritative pip parsing attempted network access")

# Observe each logical line before pip parses it so that input errors and
# option callbacks retain their actual file and starting physical line.
preprocess = req_file.preprocess
def observe_lines(content, *args, **kwargs):
    for number, text in preprocess(content, *args, **kwargs):
        location.update(source=file_stack[-1], line=number)
        observed_options.clear()
        yield number, text
req_file.preprocess = observe_lines

# Values on pip's final option object do not reveal explicit defaults (such
# as --pre or --no-index), nor all callback options. Observe optparse itself
# before its callbacks and normalization lose that information.
process_option = optparse.Option.process
def observe_option(self, opt, value, values, parser):
    observed_options.append({
        "source": location["source"],
        "line": location["line"],
        "name": self.get_opt_string(),
        "value": "" if value is None else str(value),
    })
    return process_option(self, opt, value, values, parser)
optparse.Option.process = observe_option

parse_file = req_file.RequirementsFileParser._parse_file
def observe_file(self, filename, constraint):
    source = relative(filename)
    row = {"path": source, "constraint": constraint}
    files[json.dumps(row, sort_keys=True)] = row
    file_stack.append(source)
    try:
        for line in parse_file(self, filename, constraint):
            location.update(source=source, line=line.lineno)
            if line.is_requirement:
                # Hashes are retained by InstallRequirement.hash_options.
                # Every other option, even one pip ignores on this kind of
                # line, remains visible and makes the report unacceptable.
                requirement_options[(source, line.lineno)] = [
                    item for item in observed_options if item["name"] != "--hash"
                ]
            else:
                for values, role in [(line.opts.requirements, False), (line.opts.constraints, True)]:
                    for value in values or []:
                        target = value if "://" in value else relative(os.path.join(os.path.dirname(filename), value))
                        include = {"source": source, "target": target, "value": value, "constraint": role}
                        includes[json.dumps(include, sort_keys=True)] = include
                report["options"].extend(
                    item for item in observed_options
                    if item["name"] not in ("--requirement", "--constraint")
                )
            yield line
    finally:
        file_stack.pop()
req_file.RequirementsFileParser._parse_file = observe_file

# A missing initial mount/file is an execution failure. Pip's own refusals
# while interpreting its contents are reported separately below.
os.stat(os.path.join(root, "requirements.txt"))
try:
    with PipSession() as session:
        session.request = no_network
        for parsed in req_file.parse_requirements(os.path.join(root, "requirements.txt"), session=session):
            source, number = location["source"], location["line"]
            install = install_req_from_parsed_requirement(parsed, isolated=True)
            requirement = install.req
            link = install.link
            row = {
                "source": source,
                "line": number,
                "name": canonicalize_name(requirement.name) if requirement is not None else "",
                "extras": sorted({canonicalize_name(extra) for extra in requirement.extras}) if requirement is not None else [],
                "specifier": str(requirement.specifier) if requirement is not None else "",
                "marker": str(install.markers) if install.markers else "",
                "hashes": {algorithm: sorted(set(values)) for algorithm, values in install.hash_options.items()},
                "constraint": install.constraint,
                "url": requirement.url or "" if requirement is not None else "",
                "link": str(link) if link is not None else "",
                "path": link.file_path if link is not None and link.is_file else "",
                "archive": bool(link is not None and is_archive_file(link.filename)),
                "editable": install.editable,
                "options": requirement_options[(source, number)],
            }
            report["requirements"].append(row)
except InstallationError as error:
    report["errors"].append({
        "source": location["source"],
        "line": location["line"],
        "type": type(error).__name__,
        "message": str(error),
    })

report["files"] = [files[key] for key in sorted(files)]
report["includes"] = [includes[key] for key in sorted(includes)]
print(json.dumps(report, sort_keys=True))
`
