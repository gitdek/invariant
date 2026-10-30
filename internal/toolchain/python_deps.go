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
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// PythonLibraryDir is an import-only directory: neither a Python site directory
// nor part of PATH. Application launchers append it after trusted and project
// paths, only after the base interpreter has completed isolated startup.
const PythonLibraryDir = "/opt/invariant/python-libraries"

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReportPath = "/opt/invariant/python-installation.json"
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY canonical/ /opt/invariant/canonical/
WORKDIR /tmp
RUN ["/usr/local/bin/python", "-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-cache-dir", "--only-binary=:all:", "--index-url=https://pypi.org/simple", "--target", "` + PythonLibraryDir + `", "--report", "` + pythonDepsReportPath + `", "-r", "/opt/invariant/canonical/requirements.txt", "-c", "/opt/invariant/canonical/constraints.txt"]
`

// PythonDependencies records both input representations and the actual wheel
// resolution saved by pip. Image is an immutable local execution handle, not
// portable receipt evidence; the recipe tag is never used to execute code.
type PythonDependencies struct {
	Image           string                     `json:"-"`
	Base            string                     `json:"base"`
	Recipe          string                     `json:"recipe"`
	Platform        string                     `json:"platform"`
	Inputs          []PythonDependencyInput    `json:"inputs"`
	CanonicalInputs []PythonDependencyInput    `json:"canonical_inputs"`
	Artifacts       []PythonDependencyArtifact `json:"artifacts"`
}

type PythonDependencyInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type PythonDependencyArtifact struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

type pythonFileRole struct {
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

type pythonPipReport struct {
	Schema       int                    `json:"schema"`
	PipVersion   string                 `json:"pip_version"`
	Root         string                 `json:"root"`
	Files        []pythonFileRole       `json:"files"`
	Includes     []pythonInclude        `json:"includes"`
	Requirements []pythonPipRequirement `json:"requirements"`
	Options      []pythonPipOption      `json:"options"`
	Errors       []struct {
		Source  string `json:"source"`
		Line    int    `json:"line"`
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"errors"`
}

type pythonInputs struct {
	root         string
	files        map[string][]byte
	roles        map[pythonFileRole]bool
	includes     map[pythonInclude]bool
	requirements map[pythonRequirementLocation]bool
	canonical    map[string][]byte
}

type pythonRequirementLocation struct {
	Source     string
	Line       int
	Constraint bool
}

type pythonInputLine struct {
	Text   string
	Number int
}

var pythonDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var pythonName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
var pythonNamePrefix = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
var pythonSpecifier = regexp.MustCompile(`^(===|==|!=|~=|<=|>=|<|>)[ \t]*[A-Za-z0-9][A-Za-z0-9.*+!_-]*$`)
var pythonCoding = regexp.MustCompile(`coding[ \t]*[:=]`)
var pythonOptionLiteral = regexp.MustCompile(`(^|[ \t])-[A-Za-z-]`)

func pythonHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// PythonDepsRecipe performs the same offline authoritative parse as image
// selection. The API cannot return a recipe for inputs pip has not accepted.
func PythonDepsRecipe(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	inputs, err := pythonDependencyInputs(ctx, root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.recipe(), nil
}

// PythonDepsImage installs only public PyPI wheels, during image construction.
// Reuse reads that image's original resolution rather than resolving again.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := pythonDependencyInputs(ctx, root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform, Recipe: inputs.recipe(), Inputs: pythonInputHashes(inputs.files), CanonicalInputs: pythonInputHashes(inputs.canonical)}
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
		for name, body := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(buildDir, "canonical", name), body, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		iidfile := filepath.Join(dir, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, buildDir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies from public PyPI wheels: %w\n%s", err, out)
		}
		id, err = os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("reading immutable Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(id))
	if !pythonDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("Python dependency image has no valid immutable ID: %q", env.Image)
	}
	out, err := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", env.Image, "/bin/cat", pythonDepsReportPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation evidence: %w\n%s", err, out)
	}
	env.Artifacts, err = pythonArtifacts(out)
	if err != nil {
		return nil, err
	}
	return env, nil
}

func pythonInputHashes(files map[string][]byte) []PythonDependencyInput {
	inputs := make([]PythonDependencyInput, 0, len(files))
	for name, body := range files {
		inputs = append(inputs, PythonDependencyInput{Path: name, SHA256: pythonHash(body)})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	return inputs
}

func (in *pythonInputs) recipe() string {
	h := sha256.New()
	write := func(b []byte) { fmt.Fprintf(h, "%d\n", len(b)); h.Write(b) }
	for _, value := range []string{pythonDepsDockerfile, PythonImage, pythonDepsPlatform, pythonPinnedPipParser} {
		write([]byte(value))
	}
	for _, files := range []map[string][]byte{in.files, in.canonical} {
		for _, input := range pythonInputHashes(files) {
			write([]byte(input.Path))
			write(files[input.Path])
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func pythonDependencyInputs(ctx context.Context, root string) (*pythonInputs, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(abs, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	in := &pythonInputs{root: abs, files: map[string][]byte{}, roles: map[pythonFileRole]bool{}, includes: map[pythonInclude]bool{}, requirements: map[pythonRequirementLocation]bool{}}
	if err := in.visit("requirements.txt", false, map[string]bool{}); err != nil {
		return nil, fmt.Errorf("Python requirements preflight: %w", err)
	}
	cmd := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "-v", abs+":/project:ro", "-w", "/tmp", PythonImage, "/usr/local/bin/python", "-I", "-c", pythonPinnedPipParser)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parsing Python requirements with pinned pip: %w\n%s", err, stderr.String())
	}
	var report pythonPipReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("invalid pinned pip requirements report: %w", err)
	}
	if err := in.accept(report); err != nil {
		return nil, fmt.Errorf("pinned pip requirements report refused: %w", err)
	}
	// The bytes accepted by preflight must still be those pip just read. Never
	// bind a report to an earlier version of a concurrently edited input.
	for name, before := range in.files {
		after, err := os.ReadFile(filepath.Join(abs, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(before, after) {
			return nil, fmt.Errorf("Python dependency input changed during parsing: %s", name)
		}
	}
	return in, nil
}

func (in *pythonInputs) visit(name string, constraint bool, active map[string]bool) error {
	if active[name] {
		return fmt.Errorf("include cycle at %s", name)
	}
	role := pythonFileRole{name, constraint}
	if in.roles[role] {
		return nil
	}
	if _, err := in.resolve("requirements.txt", name); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(in.root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	lines, err := pythonLogicalLines(b)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	in.files[name], in.roles[role], active[name] = b, true, true
	defer delete(active, name)
	for _, line := range lines {
		value, asConstraint, include, err := pythonIncludeValue(line.Text)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if include {
			target, err := in.resolve(name, value)
			if err != nil {
				return err
			}
			in.includes[pythonInclude{name, target, value, asConstraint}] = true
			if err := in.visit(target, asConstraint, active); err != nil {
				return err
			}
		} else {
			if err := pythonValidateRequirement(line.Text); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			in.requirements[pythonRequirementLocation{name, line.Number, constraint}] = true
		}
	}
	return nil
}

// Walk the literal reference, checking even directories a later '..' cancels.
func (in *pythonInputs) resolve(source, value string) (string, error) {
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, "\\:$\x00") {
		return "", fmt.Errorf("unsupported requirement reference %q in %s", value, source)
	}
	parts := []string{}
	if dir := path.Dir(source); dir != "." {
		parts = strings.Split(dir, "/")
	}
	components := strings.Split(value, "/")
	for i, component := range components {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("requirement reference escapes project: %q", value)
			}
			parts = parts[:len(parts)-1]
			continue
		default:
			parts = append(parts, component)
		}
		info, err := os.Lstat(filepath.Join(in.root, filepath.FromSlash(strings.Join(parts, "/"))))
		if err != nil {
			return "", fmt.Errorf("requirement reference %q: %w", value, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(components)-1 && !info.IsDir()) || (i == len(components)-1 && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("requirement reference must traverse ordinary directories to a regular file: %q", value)
		}
	}
	return strings.Join(parts, "/"), nil
}

func pythonLogicalLines(raw []byte) ([]pythonInputLine, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	body := strings.TrimPrefix(string(raw), "\ufeff")
	for i, r := range body {
		if strings.ContainsRune("\v\f\x1c\x1d\x1e\x1f\u0085\u2028\u2029\ufeff\x00", r) || (r == '\r' && (i+1 == len(body) || body[i+1] != '\n')) {
			return nil, fmt.Errorf("unsupported requirements separator U+%04X", r)
		}
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	physical := strings.Split(body, "\n")
	var lines []pythonInputLine
	continued := ""
	continuing, firstLine := false, 0
	for i, line := range physical {
		if !continuing {
			firstLine = i + 1
		}
		if comment := strings.IndexByte(line, '#'); comment >= 0 && pythonCoding.MatchString(line[comment:]) {
			return nil, fmt.Errorf("encoding declarations are unsupported")
		}
		if strings.Contains(line, "\\") {
			if strings.Count(line, "\\") != 1 || !strings.HasSuffix(line, "\\") || i >= len(physical)-1 || (i == len(physical)-2 && physical[i+1] == "") || strings.Contains(line, "#") {
				return nil, fmt.Errorf("only a single trailing continuation backslash is supported")
			}
			continued += strings.TrimSuffix(line, "\\")
			continuing = true
			continue
		}
		line, continued = continued+line, ""
		continuing = false
		var quote byte
		for j := 0; j < len(line); j++ {
			switch {
			case quote != 0:
				if line[j] == '#' {
					return nil, fmt.Errorf("comment characters in marker literals are unsupported")
				}
				if line[j] == quote {
					quote = 0
				}
			case line[j] == '\'' || line[j] == '"':
				quote = line[j]
			case line[j] == '#' && (j == 0 || line[j-1] == ' ' || line[j-1] == '\t'):
				line = line[:j]
				j = len(line)
			}
		}
		line = strings.Trim(line, " \t")
		if line != "" {
			lines = append(lines, pythonInputLine{line, firstLine})
		}
	}
	if continued != "" {
		return nil, fmt.Errorf("dangling continuation")
	}
	return lines, nil
}

func pythonIncludeValue(line string) (string, bool, bool, error) {
	for _, option := range []struct {
		flag       string
		constraint bool
	}{{"--requirement", false}, {"--constraint", true}, {"-r", false}, {"-c", true}} {
		if !strings.HasPrefix(line, option.flag) {
			continue
		}
		value := strings.TrimPrefix(line, option.flag)
		if strings.HasPrefix(value, "=") && len(option.flag) > 2 {
			value = value[1:]
			if value == "" || value[0] == ' ' || value[0] == '\t' {
				return "", false, true, fmt.Errorf("empty include value")
			}
		} else {
			if len(option.flag) > 2 && value != "" && value[0] != ' ' && value[0] != '\t' {
				return "", false, true, fmt.Errorf("unsupported include option")
			}
			value = strings.TrimLeft(value, " \t")
		}
		if value == "" || strings.ContainsAny(value, "'\"") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
			return "", false, true, fmt.Errorf("include needs one literal local filename")
		}
		return value, option.constraint, true, nil
	}
	return "", false, false, nil
}

// This is deliberately a preflight subset, never the authority for installation.
// Canonical text is constructed exclusively from pip's semantic report below.
func pythonValidateRequirement(line string) error {
	bad := func() error { return fmt.Errorf("unsupported named PyPI requirement %q", line) }
	if strings.ContainsAny(line, "\\#\r\n$@/\x00") {
		return bad()
	}
	if boundary := strings.Index(line, "--"); boundary >= 0 {
		if boundary == 0 || line[boundary-1] != ' ' {
			return bad()
		}
		start := boundary
		for start > 0 && (line[start-1] == ' ' || line[start-1] == '\t') {
			start--
		}
		if strings.Contains(line[start:boundary], "\t") {
			return bad()
		}
		for _, token := range strings.Split(line[boundary:], " ") {
			if token != "" && (!strings.HasPrefix(token, "--hash=") || !pythonDigest.MatchString(strings.TrimPrefix(token, "--hash="))) {
				return bad()
			}
		}
		line = strings.TrimSpace(line[:boundary])
	}
	head, marker, hasMarker := strings.Cut(line, ";")
	head = strings.Trim(head, " \t")
	name := pythonNamePrefix.FindString(head)
	if !pythonName.MatchString(name) {
		return bad()
	}
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return bad()
		}
	}
	rest := strings.Trim(head[len(name):], " \t")
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
	}
	if rest != "" {
		for _, specifier := range strings.Split(rest, ",") {
			if !pythonSpecifier.MatchString(strings.Trim(specifier, " \t")) {
				return bad()
			}
		}
	}
	if hasMarker && !pythonValidMarker(strings.Trim(marker, " \t")) {
		return bad()
	}
	return nil
}

func pythonValidMarker(marker string) bool {
	var tokens []string
	for i := 0; i < len(marker); {
		if marker[i] == ' ' || marker[i] == '\t' {
			i++
			continue
		}
		start := i
		if marker[i] == '\'' || marker[i] == '"' {
			quote := marker[i]
			i++
			for i < len(marker) && marker[i] != quote {
				i++
			}
			if i == len(marker) || pythonOptionLiteral.MatchString(marker[start+1:i]) || strings.ContainsAny(marker[start+1:i], "\\#\r\n") {
				return false
			}
			i++
		} else if strings.ContainsRune("()", rune(marker[i])) {
			i++
		} else if strings.ContainsRune("<>=!~", rune(marker[i])) {
			i++
			for i < len(marker) && strings.ContainsRune("<>=!~", rune(marker[i])) {
				i++
			}
		} else {
			for i < len(marker) && ((marker[i] >= 'a' && marker[i] <= 'z') || marker[i] == '_') {
				i++
			}
			if i == start {
				return false
			}
		}
		tokens = append(tokens, marker[start:i])
	}
	pos := 0
	atom := func() bool {
		if pos == len(tokens) {
			return false
		}
		t := tokens[pos]
		pos++
		return strings.HasPrefix(t, "'") || strings.HasPrefix(t, "\"") || strings.Contains("|python_version|python_full_version|os_name|sys_platform|platform_release|platform_system|platform_version|platform_machine|platform_python_implementation|implementation_name|implementation_version|extra|", "|"+t+"|")
	}
	var expression, term func() bool
	term = func() bool {
		if pos < len(tokens) && tokens[pos] == "(" {
			pos++
			if !expression() || pos == len(tokens) || tokens[pos] != ")" {
				return false
			}
			pos++
			return true
		}
		if !atom() || pos == len(tokens) {
			return false
		}
		op := tokens[pos]
		pos++
		if op == "not" {
			if pos == len(tokens) || tokens[pos] != "in" {
				return false
			}
			pos++
		} else if !strings.Contains("|==|!=|<=|>=|<|>|~=|===|in|", "|"+op+"|") {
			return false
		}
		return atom()
	}
	expression = func() bool {
		if !term() {
			return false
		}
		for pos < len(tokens) && (tokens[pos] == "and" || tokens[pos] == "or") {
			pos++
			if !term() {
				return false
			}
		}
		return true
	}
	return expression() && pos == len(tokens)
}

func (in *pythonInputs) accept(report pythonPipReport) error {
	if report.Schema != 1 || report.PipVersion == "" || report.Root != "requirements.txt" || report.Files == nil || report.Includes == nil || report.Requirements == nil || report.Options == nil || report.Errors == nil {
		return fmt.Errorf("missing or unsupported report schema, pip identity or records")
	}
	if len(report.Errors) != 0 {
		return fmt.Errorf("%s:%d: %s: %s", report.Errors[0].Source, report.Errors[0].Line, report.Errors[0].Type, report.Errors[0].Message)
	}
	if len(report.Options) != 0 {
		return fmt.Errorf("unsupported pip option %s", report.Options[0].Name)
	}
	roles := map[pythonFileRole]bool{}
	for _, file := range report.Files {
		if !in.roles[file] {
			return fmt.Errorf("unexpected file or include role %q", file.Path)
		}
		roles[file] = true
	}
	if len(roles) != len(in.roles) {
		return fmt.Errorf("incomplete followed-file report")
	}
	includes := map[pythonInclude]bool{}
	for _, include := range report.Includes {
		if !in.includes[include] {
			return fmt.Errorf("unexpected include %q from %q", include.Value, include.Source)
		}
		includes[include] = true
	}
	if len(includes) != len(in.includes) {
		return fmt.Errorf("incomplete include report")
	}
	lines := map[bool]map[string]bool{false: {}, true: {}}
	requirements := map[pythonRequirementLocation]bool{}
	for _, req := range report.Requirements {
		location := pythonRequirementLocation{req.Source, req.Line, req.Constraint}
		if !roles[pythonFileRole{req.Source, req.Constraint}] || !in.requirements[location] || !pythonName.MatchString(req.Name) || req.URL != "" || req.Link != "" || req.Path != "" || req.Archive || req.Editable || req.Options == nil || len(req.Options) != 0 || req.Hashes == nil || req.Extras == nil {
			return fmt.Errorf("unsupported or incomplete pip requirement from %s:%d", req.Source, req.Line)
		}
		line := req.Name
		extras := append([]string(nil), req.Extras...)
		sort.Strings(extras)
		for _, extra := range extras {
			if !pythonName.MatchString(extra) {
				return fmt.Errorf("unsupported requirement extra %q", extra)
			}
		}
		if len(extras) != 0 {
			line += "[" + strings.Join(extras, ",") + "]"
		}
		line += req.Specifier
		if req.Marker != "" {
			line += "; " + req.Marker
		}
		for algorithm, hashes := range req.Hashes {
			if algorithm != "sha256" || len(hashes) == 0 {
				return fmt.Errorf("unsupported pip hash algorithm or empty hashes %q", algorithm)
			}
		}
		hashes := append([]string(nil), req.Hashes["sha256"]...)
		sort.Strings(hashes)
		for i, hash := range hashes {
			if !pythonDigest.MatchString("sha256:" + hash) {
				return fmt.Errorf("invalid pip SHA-256 hash")
			}
			if i == 0 || hash != hashes[i-1] {
				line += " --hash=sha256:" + hash
			}
		}
		if err := pythonValidateRequirement(line); err != nil {
			return err
		}
		lines[req.Constraint][line] = true
		requirements[location] = true
	}
	if len(requirements) != len(in.requirements) {
		return fmt.Errorf("incomplete requirement records")
	}
	in.canonical = map[string][]byte{}
	for constraint, filename := range map[bool]string{false: "requirements.txt", true: "constraints.txt"} {
		ordered := make([]string, 0, len(lines[constraint]))
		for line := range lines[constraint] {
			ordered = append(ordered, line)
		}
		sort.Strings(ordered)
		body := strings.Join(ordered, "\n")
		if len(ordered) > 0 {
			body += "\n"
		}
		in.canonical[filename] = []byte(body)
	}
	return nil
}

func pythonArtifacts(raw []byte) ([]PythonDependencyArtifact, error) {
	var report struct {
		Version    string `json:"version"`
		PipVersion string `json:"pip_version"`
		Install    []struct {
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
			DownloadInfo struct {
				URL         string `json:"url"`
				ArchiveInfo struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
			Direct bool `json:"is_direct"`
		} `json:"install"`
	}
	if err := json.Unmarshal(raw, &report); err != nil || report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("missing or malformed Python dependency installation report")
	}
	artifacts := make([]PythonDependencyArtifact, 0, len(report.Install))
	seen := map[string]bool{}
	for _, installed := range report.Install {
		u, err := url.Parse(installed.DownloadInfo.URL)
		digest := "sha256:" + installed.DownloadInfo.ArchiveInfo.Hashes["sha256"]
		if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || installed.Direct || !pythonName.MatchString(installed.Metadata.Name) || installed.Metadata.Version == "" || !pythonDigest.MatchString(digest) || !strings.HasSuffix(u.Path, ".whl") {
			return nil, fmt.Errorf("incomplete or non-PyPI wheel evidence for %q", installed.Metadata.Name)
		}
		name := regexp.MustCompile(`[-_.]+`).ReplaceAllString(strings.ToLower(installed.Metadata.Name), "-")
		if seen[name] {
			return nil, fmt.Errorf("duplicate installed Python distribution %s", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonDependencyArtifact{Name: name, Version: installed.Metadata.Version, Filename: path.Base(u.Path), SHA256: digest})
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

// The helper observes pip's parsed lines as well as its InstallRequirement
// constructor. parse_requirements alone omits include-only and option-only
// lines. No application code is imported, and markers remain unevaluated.
const pythonPinnedPipParser = `
import json
import os
import optparse
import re
import pip
from pip._internal.network.session import PipSession
from pip._internal.req.req_file import RequirementsFileParser, parse_requirements
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._vendor.packaging.utils import canonicalize_name

report = dict(schema=1, pip_version=pip.__version__, root="requirements.txt", files=[], includes=[], requirements=[], options=[], errors=[])
root = "/project"
current_source, current_line = "requirements.txt", 1
file_rows, include_rows, options_by_line = {}, {}, {}

def relative(filename):
    return os.path.relpath(os.path.normpath(filename), root)

def error_record(error, source=None, line=None):
    report["errors"].append(dict(source=source or current_source, line=line or current_line, type=type(error).__name__, message=str(error)))

def no_network(*args, **kwargs):
    raise RuntimeError("pip requirements parsing cannot access the network")

# Observe options when pip's own optparse parser consumes them. Comparing final
# values with defaults would miss explicitly supplied default-valued options,
# including --no-binary :none: and a repeated public --index-url.
pending_options = []
option_process = optparse.Option.process
def observe_option(self, opt, value, values, parser):
    name = next((name for name in self._long_opts if name.startswith("--")), opt)
    pending_options.append((self.dest, name, value))
    return option_process(self, opt, value, values, parser)
optparse.Option.process = observe_option

parse_file = RequirementsFileParser._parse_file
def observe_file(self, filename, constraint):
    global current_source, current_line, pending_options
    source = relative(filename)
    row = dict(path=source, constraint=constraint)
    file_rows[(source, constraint)] = row
    current_source, current_line = source, 1
    try:
        iterator = iter(parse_file(self, filename, constraint))
        while True:
            current_source = source
            pending_options = []
            try:
                line = next(iterator)
            except StopIteration:
                break
            current_source, current_line = source, line.lineno
            options = []
            for destination, name, value in pending_options:
                if line.is_requirement and destination in ("hashes", "editables"):
                    continue
                if not line.is_requirement and destination in ("requirements", "constraints"):
                    continue
                options.append(dict(source=source, line=line.lineno, name=name, value=repr(value)))
            options_by_line[(source, line.lineno)] = options
            if not line.is_requirement:
                report["options"].extend(options)
                for values, role in ((line.opts.requirements, False), (line.opts.constraints, True)):
                    for value in values or []:
                        if not value or ":" in value or os.path.isabs(value):
                            raise ValueError("unsupported requirement include " + repr(value))
                        target = relative(os.path.join(os.path.dirname(filename), value))
                        row = dict(source=source, target=target, value=value, constraint=role)
                        include_rows[(source, target, value, role)] = row
            yield line
    except Exception as error:
        error_record(error, source, current_line)
        raise

RequirementsFileParser._parse_file = observe_file
try:
    with PipSession() as session:
        session.request = no_network
        for parsed in parse_requirements(root + "/requirements.txt", session=session):
            match = re.match(r"line ([0-9]+) of (.*)", parsed.line_source or "")
            source = relative(match.group(2)) if match else current_source
            number = int(match.group(1)) if match else current_line
            current_source, current_line = source, number
            try:
                install = install_req_from_parsed_requirement(parsed, isolated=True)
                req = install.req
                link = str(install.link) if install.link else ""
                report["requirements"].append(dict(
                    source=source, line=number,
                    name=canonicalize_name(req.name) if req else "",
                    extras=sorted({canonicalize_name(extra) for extra in req.extras}) if req else [],
                    specifier=str(req.specifier) if req else "",
                    marker=str(install.markers) if install.markers else "",
                    hashes={algorithm: sorted(set(values)) for algorithm, values in install.hash_options.items()},
                    constraint=install.constraint, url=req.url or "" if req else "", link=link,
                    path=install.link.file_path if install.link and install.link.is_file else "",
                    archive=bool(install.link and install.link.is_wheel), editable=install.editable,
                    options=options_by_line.get((source, number), [])))
            except Exception as error:
                error_record(error, source, number)
except Exception as error:
    if not report["errors"]:
        error_record(error)
report["files"] = [file_rows[key] for key in sorted(file_rows)]
report["includes"] = [include_rows[key] for key in sorted(include_rows)]
print(json.dumps(report, sort_keys=True))
`
