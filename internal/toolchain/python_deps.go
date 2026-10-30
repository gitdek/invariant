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
	"unicode"
	"unicode/utf8"
)

// PythonLibraryDir is an import-only directory, never a Python site directory
// or a source of gate commands. The application launcher adds it after startup.
const PythonLibraryDir = "/opt/invariant/python-libraries"

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant/python-dependencies.json"

// Keep the installer outside the input directory, and never copy application
// files into this network-enabled build. pip's target schemes confine both
// libraries and console scripts below PythonLibraryDir, away from the base
// interpreter, trusted commands, and the installation report.
const pythonDepsDockerfile = "FROM " + PythonImage + `
WORKDIR /tmp
COPY canonical/ /opt/invariant/canonical/
RUN /usr/local/bin/python -I -m pip --isolated --disable-pip-version-check install --index-url https://pypi.org/simple --only-binary=:all: --no-cache-dir --no-compile --target ` + PythonLibraryDir + ` --report ` + pythonDepsReport + ` --requirement /opt/invariant/canonical/requirements.txt --constraint /opt/invariant/canonical/constraints.txt
`

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

// PythonDependencies records portable provenance. Image is only the immutable
// local execution handle; a mutable recipe tag never identifies evidence.
type PythonDependencies struct {
	Image           string                     `json:"-"`
	Base            string                     `json:"base"`
	Recipe          string                     `json:"recipe"`
	Platform        string                     `json:"platform"`
	Inputs          []PythonDependencyInput    `json:"inputs"`
	CanonicalInputs []PythonDependencyInput    `json:"canonical_inputs"`
	Artifacts       []PythonDependencyArtifact `json:"artifacts"`
}

type pythonDependencyFiles struct {
	root      string
	inputs    map[string][]byte
	roles     map[pythonParsedFile]bool
	includes  map[pythonParsedInclude]bool
	lines     map[pythonParsedFile]map[int]bool
	canonical map[string][]byte
	evidence  PythonDependencies
}

// PythonDepsRecipe validates original files before Docker, then asks only the
// pinned base's pip to determine their canonical installation semantics.
func PythonDepsRecipe(root string) (string, error) {
	files, err := pythonDependencyRecipe(context.Background(), root)
	if err != nil || files == nil {
		return "", err
	}
	return files.evidence.Recipe, nil
}

func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	files, err := pythonDependencyRecipe(ctx, root)
	if err != nil || files == nil {
		return nil, err
	}
	tag := "invariant-python-deps:" + strings.TrimPrefix(files.evidence.Recipe, "sha256:")
	// Resolve the cache key once. Every later read and execution uses that ID,
	// including if another builder replaces the tag immediately afterwards.
	id, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if inspectErr != nil {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		if err := os.Mkdir(filepath.Join(dir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		for filename, body := range files.canonical {
			if err := os.WriteFile(filepath.Join(dir, "canonical", filename), body, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		// The iidfile is outside the build context and is not a mutable tag.
		iid, err := os.CreateTemp("", "invariant-python-image-")
		if err != nil {
			return nil, err
		}
		iid.Close()
		defer os.Remove(iid.Name())
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iid.Name(), "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies: %w\n%s", err, out)
		}
		id, err = os.ReadFile(iid.Name())
		if err != nil {
			return nil, fmt.Errorf("reading the built Python image ID: %w", err)
		}
	}
	files.evidence.Image = strings.TrimSpace(string(id))
	if !pythonSHA256.MatchString(files.evidence.Image) {
		return nil, fmt.Errorf("Python dependency image has no immutable SHA-256 ID")
	}
	raw, err := ReadPythonDepsReport(ctx, files.evidence.Image)
	if err != nil {
		return nil, err
	}
	files.evidence.Artifacts, err = pythonInstalledArtifacts(raw)
	if err != nil {
		return nil, err
	}
	return &files.evidence, nil
}

// ReadPythonDepsReport reads genuine image evidence without applying source
// admission policy. Production validates the result separately; fixture images
// can use this same trusted reader without pretending they came from PyPI.
func ReadPythonDepsReport(ctx context.Context, image string) ([]byte, error) {
	if !pythonSHA256.MatchString(image) {
		return nil, fmt.Errorf("reading Python dependency evidence requires an immutable image ID")
	}
	cmd := Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", "--workdir", "/tmp", image, "/bin/cat", pythonDepsReport)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading Python installation report: %w\n%s", err, stderr.String())
	}
	return raw, nil
}

var pythonDistributionSeparators = regexp.MustCompile(`[-_.]+`)

// Installation evidence comes from pip's saved report, including transitive
// wheels. An explicit empty install array is a valid resolution; an absent
// report or incomplete artifact is not evidence of an empty environment.
func pythonInstalledArtifacts(raw []byte) ([]PythonDependencyArtifact, error) {
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
			IsDirect *bool `json:"is_direct"`
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("invalid Python installation report: %w", err)
	}
	if report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("incomplete Python installation report: required version, pip_version and install evidence")
	}
	artifacts := make([]PythonDependencyArtifact, 0, len(report.Install))
	seen := map[string]bool{}
	for i, installed := range report.Install {
		name, version := installed.Metadata.Name, installed.Metadata.Version
		if !pythonName.MatchString(name) || version == "" || strings.ContainsAny(version, "\x00\r\n\t /\\") || installed.IsDirect == nil {
			return nil, fmt.Errorf("incomplete Python installation artifact %d: required name, version and is_direct", i)
		}
		location, err := url.Parse(installed.DownloadInfo.URL)
		if err != nil || location.Scheme != "https" || location.Host != "files.pythonhosted.org" || location.User != nil || location.RawQuery != "" || location.Fragment != "" || *installed.IsDirect {
			return nil, fmt.Errorf("Python installation artifact %s is not a public-PyPI wheel", name)
		}
		filename := path.Base(location.Path)
		if !strings.HasSuffix(filename, ".whl") || strings.ContainsAny(filename, "\x00\r\n\t /\\") || len(strings.Split(strings.TrimSuffix(filename, ".whl"), "-")) < 5 {
			return nil, fmt.Errorf("Python installation artifact %s has no valid wheel filename", name)
		}
		hash := "sha256:" + installed.DownloadInfo.ArchiveInfo.Hashes["sha256"]
		if !pythonSHA256.MatchString(hash) {
			return nil, fmt.Errorf("Python installation artifact %s is missing a valid SHA-256 wheel hash", name)
		}
		identity := strings.ToLower(pythonDistributionSeparators.ReplaceAllString(name, "-"))
		if seen[identity] {
			return nil, fmt.Errorf("Python installation report repeats distribution %s", name)
		}
		seen[identity] = true
		artifacts = append(artifacts, PythonDependencyArtifact{Name: name, Version: version, Filename: filename, SHA256: hash})
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

func pythonDigest(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

var pythonSHA256 = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var pythonName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
var pythonNamePrefix = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
var pythonSpecifier = regexp.MustCompile(`^(===|==|!=|~=|<=|>=|<|>)[ \t]*[vV]?[0-9][A-Za-z0-9.*+!_-]*(?:\.[A-Za-z0-9*+!_-]+)*$`)
var pythonEncoding = regexp.MustCompile(`coding[ \t]*[:=]`)
var pythonHash = regexp.MustCompile(`^--hash=sha256:[0-9a-f]{64}$`)

func pythonDependencyRecipe(ctx context.Context, root string) (*pythonDependencyFiles, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	f := &pythonDependencyFiles{root: root, inputs: map[string][]byte{}, roles: map[pythonParsedFile]bool{}, includes: map[pythonParsedInclude]bool{}, lines: map[pythonParsedFile]map[int]bool{}}
	if err := f.preflight("requirements.txt", false, map[string]bool{}); err != nil {
		return nil, fmt.Errorf("Python requirements: %w", err)
	}
	cmd := Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", "--volume", root+":/project:ro", "--workdir", "/tmp", "--entrypoint", "/usr/local/bin/python",
		PythonImage, "-I", "-B", "-c", pythonPipParser)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parsing Python requirements with pinned pip: %w\n%s", err, stderr.String())
	}
	report, err := decodePythonPipReport(raw)
	if err != nil {
		return nil, err
	}
	f.canonical, err = f.acceptReport(report)
	if err != nil {
		return nil, err
	}
	f.evidence = PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform, Inputs: []PythonDependencyInput{}, CanonicalInputs: []PythonDependencyInput{}}
	h := sha256.New()
	add := func(b []byte) { fmt.Fprintf(h, "%d\n", len(b)); h.Write(b) }
	for _, s := range []string{pythonDepsDockerfile, PythonImage, pythonDepsPlatform, pythonPipParser} {
		add([]byte(s))
	}
	for _, set := range []struct {
		name string
		data map[string][]byte
		out  *[]PythonDependencyInput
	}{{"original", f.inputs, &f.evidence.Inputs}, {"canonical", f.canonical, &f.evidence.CanonicalInputs}} {
		add([]byte(set.name))
		keys := make([]string, 0, len(set.data))
		for name := range set.data {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			add([]byte(name))
			add(set.data[name])
			*set.out = append(*set.out, PythonDependencyInput{Path: name, SHA256: pythonDigest(set.data[name])})
		}
	}
	f.evidence.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return f, nil
}

// resolve checks every traversed component before cleaning '..': even a
// canceled symlink, missing directory, or temporary escape is inadmissible.
func (f *pythonDependencyFiles) resolve(source, value string) (string, error) {
	if value == "" || path.IsAbs(value) || strings.ContainsAny(value, "\\:$\x00\r\n\"'") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("unsupported local include %q in %s", value, source)
	}
	parts := []string{}
	if source != "" && path.Dir(source) != "." {
		parts = strings.Split(path.Dir(source), "/")
	}
	for _, part := range strings.Split(value, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("include %q escapes the project", value)
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
			info, err := os.Lstat(filepath.Join(f.root, filepath.FromSlash(strings.Join(parts, "/"))))
			if err != nil {
				return "", fmt.Errorf("include %q: %w", value, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("include %q traverses a symlink", value)
			}
			// A non-directory before a following slash is caught below by
			// opening the uncleaned spelling, including canceled components.
		}
	}
	name := strings.Join(parts, "/")
	literal := f.root + "/"
	if source != "" {
		literal += path.Dir(source) + "/"
	}
	literal += value
	info, err := os.Stat(literal)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return "", fmt.Errorf("include %q: %w", value, err)
	}
	return name, nil
}

func (f *pythonDependencyFiles) preflight(filename string, constraint bool, active map[string]bool) error {
	name, err := f.resolve("", filename)
	if err != nil {
		return err
	}
	if active[name] {
		return fmt.Errorf("requirement include cycle at %s", name)
	}
	role := pythonParsedFile{Path: name, Constraint: constraint}
	if f.roles[role] {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	lines, err := pythonLogicalLines(b)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	f.inputs[name], f.roles[role], f.lines[role] = b, true, map[int]bool{}
	active[name] = true
	defer delete(active, name)
	for _, line := range lines {
		body, err := pythonWithoutComment(line.body)
		if err != nil {
			return fmt.Errorf("%s:%d: %w", name, line.number, err)
		}
		body = strings.Trim(body, " \t")
		if body == "" {
			continue
		}
		if strings.HasPrefix(body, "-") {
			value, included, asConstraint := "", false, false
			for _, flag := range []struct {
				name       string
				constraint bool
			}{{"--requirement", false}, {"--constraint", true}, {"-r", false}, {"-c", true}} {
				if !strings.HasPrefix(body, flag.name) {
					continue
				}
				rest := strings.TrimPrefix(body, flag.name)
				if strings.HasPrefix(rest, "=") {
					rest = rest[1:]
					if strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
						break
					}
				} else if len(flag.name) > 2 && (rest == "" || (rest[0] != ' ' && rest[0] != '\t')) {
					break
				}
				value, included, asConstraint = strings.Trim(rest, " \t"), true, flag.constraint
				break
			}
			if !included || value == "" {
				return fmt.Errorf("%s:%d: unsupported requirement option", name, line.number)
			}
			target, err := f.resolve(name, value)
			if err != nil {
				return err
			}
			f.includes[pythonParsedInclude{Source: name, Target: target, Value: value, Constraint: asConstraint}] = true
			if err := f.preflight(target, asConstraint, active); err != nil {
				return err
			}
		} else {
			if err := validatePythonRequirement(body); err != nil {
				return fmt.Errorf("%s:%d: %w", name, line.number, err)
			}
			f.lines[role][line.number] = true
		}
	}
	return nil
}

type pythonLogicalLine struct {
	number int
	body   string
}

func pythonLogicalLines(b []byte) ([]pythonLogicalLine, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must use valid UTF-8")
	}
	s := string(b)
	if strings.ContainsAny(s, "\x00\v\f\u001c\u001d\u001e\u001f\u0085\u2028\u2029") {
		return nil, fmt.Errorf("unsupported line separator or control character")
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.Contains(s, "\r") {
		return nil, fmt.Errorf("bare CR is not a supported line ending")
	}
	s = strings.TrimPrefix(s, "\ufeff")
	if strings.Contains(s, "\ufeff") {
		return nil, fmt.Errorf("UTF-8 BOM is only allowed at the start")
	}
	var lines []pythonLogicalLine
	joined, first, pending := "", 1, false
	physical := strings.Split(s, "\n")
	for i, line := range physical {
		if hash := strings.IndexByte(line, '#'); hash >= 0 && pythonEncoding.MatchString(line[hash:]) {
			return nil, fmt.Errorf("encoding declarations are not supported")
		}
		if !pending {
			first = i + 1
		}
		if strings.Contains(line, "\\") {
			if strings.Count(line, "\\") != 1 || !strings.HasSuffix(line, "\\") || i == len(physical)-1 || (i == len(physical)-2 && physical[i+1] == "") {
				return nil, fmt.Errorf("only a single trailing continuation backslash is supported")
			}
			joined += strings.TrimSuffix(line, "\\")
			pending = true
			continue
		}
		joined += line
		lines = append(lines, pythonLogicalLine{first, joined})
		joined, pending = "", false
	}
	if pending {
		return nil, fmt.Errorf("incomplete line continuation")
	}
	return lines, nil
}

func pythonWithoutComment(s string) (string, error) {
	var quote rune
	for i, r := range s {
		if quote != 0 {
			if r == '#' {
				return "", fmt.Errorf("marker literals cannot contain comments")
			}
			if r == quote {
				quote = 0
			}
		} else if r == '\'' || r == '"' {
			quote = r
		} else if r == '#' {
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return s[:i], nil
			}
			return "", fmt.Errorf("unsupported comment boundary")
		}
	}
	if quote != 0 {
		return "", fmt.Errorf("unterminated marker literal")
	}
	return s, nil
}

func validatePythonRequirement(s string) error {
	if strings.ContainsAny(s, "\\\r\n\x00$") {
		return fmt.Errorf("unsupported requirement syntax")
	}
	// pip's option boundary is ASCII space, not generic whitespace. Do not
	// repair a tab or Unicode whitespace into an accepted --hash boundary.
	if at := strings.Index(s, "--hash"); at >= 0 {
		if at == 0 || s[at-1] != ' ' {
			return fmt.Errorf("hash options require ASCII spaces")
		}
		start := at
		for start > 0 && s[start-1] == ' ' {
			start--
		}
		if start > 0 && (unicode.IsSpace(rune(s[start-1])) || s[start-1] == '\t') {
			return fmt.Errorf("hash options require ASCII spaces")
		}
		for _, token := range strings.Split(s[at:], " ") {
			if token != "" && !pythonHash.MatchString(token) {
				return fmt.Errorf("unsupported hash option %q", token)
			}
		}
		s = strings.TrimRight(s[:start], " ")
	}
	name := pythonNamePrefix.FindString(s)
	if !pythonName.MatchString(name) {
		return fmt.Errorf("expected a named PyPI requirement")
	}
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return fmt.Errorf("archive requirements are not supported")
		}
	}
	rest := strings.Trim(s[len(name):], " \t")
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return fmt.Errorf("unterminated extras")
		}
		for _, extra := range strings.Split(rest[1:end], ",") {
			if !pythonName.MatchString(strings.Trim(extra, " \t")) {
				return fmt.Errorf("invalid requirement extra")
			}
		}
		rest = strings.Trim(rest[end+1:], " \t")
	}
	spec, marker, hasMarker := strings.Cut(rest, ";")
	spec = strings.Trim(spec, " \t")
	if strings.HasPrefix(spec, "(") && strings.HasSuffix(spec, ")") {
		spec = strings.Trim(spec[1:len(spec)-1], " \t")
	}
	if spec != "" {
		for _, item := range strings.Split(spec, ",") {
			if !pythonSpecifier.MatchString(strings.Trim(item, " \t")) {
				return fmt.Errorf("unsupported version specifier %q", item)
			}
		}
	}
	if hasMarker {
		if err := validatePythonMarker(marker); err != nil {
			return err
		}
	}
	return nil
}

type pythonMarkerParser struct {
	tokens []string
	next   int
}

func validatePythonMarker(s string) error {
	p := pythonMarkerParser{}
	for i := 0; i < len(s); {
		c := s[i]
		if c == ' ' || c == '\t' {
			i++
			continue
		}
		if c == '\'' || c == '"' {
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				return fmt.Errorf("unterminated marker literal")
			}
			end += i + 1
			value := s[i+1 : end]
			if strings.ContainsAny(value, "#\\\r\n") || strings.Contains(value, "--") {
				return fmt.Errorf("unsupported marker literal")
			}
			for _, word := range strings.Fields(value) {
				if strings.HasPrefix(word, "-") {
					return fmt.Errorf("option-like marker literal")
				}
			}
			p.tokens = append(p.tokens, s[i:end+1])
			i = end + 1
		} else if strings.ContainsRune("()", rune(c)) {
			p.tokens = append(p.tokens, string(c))
			i++
		} else if strings.ContainsRune("<>=!~", rune(c)) {
			j := i + 1
			for j < len(s) && strings.ContainsRune("<>=!~", rune(s[j])) {
				j++
			}
			p.tokens = append(p.tokens, s[i:j])
			i = j
		} else if (c >= 'a' && c <= 'z') || c == '_' {
			j := i + 1
			for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || s[j] == '_') {
				j++
			}
			p.tokens = append(p.tokens, s[i:j])
			i = j
		} else {
			return fmt.Errorf("unsupported marker character")
		}
	}
	if !p.expression() || p.next != len(p.tokens) {
		return fmt.Errorf("invalid requirement marker")
	}
	return nil
}

func (p *pythonMarkerParser) take(value string) bool {
	if p.next < len(p.tokens) && p.tokens[p.next] == value {
		p.next++
		return true
	}
	return false
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
	operator := false
	for _, op := range []string{"===", "==", "!=", "~=", "<=", ">=", "<", ">", "in"} {
		if p.take(op) {
			operator = true
			break
		}
	}
	if !operator {
		operator = p.take("not") && p.take("in")
	}
	return operator && p.value()
}
func (p *pythonMarkerParser) value() bool {
	if p.next >= len(p.tokens) {
		return false
	}
	v := p.tokens[p.next]
	if strings.HasPrefix(v, "'") || strings.HasPrefix(v, "\"") {
		p.next++
		return true
	}
	switch v {
	case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra":
		p.next++
		return true
	}
	return false
}

// This versioned boundary is deliberately explicit. A zero value is not a
// substitute for a missing pip field, especially for specifiers and markers.
type pythonParsedReport struct {
	Schema       int                       `json:"schema"`
	PipVersion   string                    `json:"pip_version"`
	Root         string                    `json:"root"`
	Files        []pythonParsedFile        `json:"files"`
	Includes     []pythonParsedInclude     `json:"includes"`
	Requirements []pythonParsedRequirement `json:"requirements"`
	Options      []pythonParsedOption      `json:"options"`
	Errors       []pythonParsedError       `json:"errors"`
}
type pythonParsedFile struct {
	Path       string `json:"path"`
	Constraint bool   `json:"constraint"`
}
type pythonParsedInclude struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Value      string `json:"value"`
	Constraint bool   `json:"constraint"`
}
type pythonParsedRequirement struct {
	Source     string               `json:"source"`
	Line       int                  `json:"line"`
	Name       string               `json:"name"`
	Extras     []string             `json:"extras"`
	Specifier  string               `json:"specifier"`
	Marker     string               `json:"marker"`
	Hashes     map[string][]string  `json:"hashes"`
	Constraint bool                 `json:"constraint"`
	URL        string               `json:"url"`
	Link       string               `json:"link"`
	Path       string               `json:"path"`
	Archive    bool                 `json:"archive"`
	Editable   bool                 `json:"editable"`
	Options    []pythonParsedOption `json:"options"`
}
type pythonParsedOption struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}
type pythonParsedError struct {
	Source  string `json:"source"`
	Line    int    `json:"line"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func requiredPythonFields(raw json.RawMessage, location string, names ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("invalid pip parser report %s: %w", location, err)
	}
	for _, name := range names {
		value, ok := object[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("incomplete pip parser report: required field %s.%s is missing or null", location, name)
		}
	}
	return object, nil
}

func decodePythonPipReport(raw []byte) (*pythonParsedReport, error) {
	top, err := requiredPythonFields(raw, "report", "schema", "pip_version", "root", "files", "includes", "requirements", "options", "errors")
	if err != nil {
		return nil, err
	}
	optionFields := []string{"source", "line", "name", "value"}
	// Complete structural validation of every row precedes all policy checks,
	// including forbidden options and pip errors. Thus a malformed later row
	// cannot hide behind an earlier semantic refusal.
	for _, group := range []struct {
		name   string
		fields []string
	}{
		{"files", []string{"path", "constraint"}},
		{"includes", []string{"source", "target", "value", "constraint"}},
		{"requirements", []string{"source", "line", "name", "extras", "specifier", "marker", "hashes", "constraint", "url", "link", "path", "archive", "editable", "options"}},
		{"options", optionFields},
		{"errors", []string{"source", "line", "type", "message"}},
	} {
		var rows []json.RawMessage
		if err := json.Unmarshal(top[group.name], &rows); err != nil {
			return nil, fmt.Errorf("invalid pip parser %s: %w", group.name, err)
		}
		for i, rawRow := range rows {
			location := fmt.Sprintf("%s[%d]", group.name, i)
			row, err := requiredPythonFields(rawRow, location, group.fields...)
			if err != nil {
				return nil, err
			}
			if group.name == "requirements" {
				var options []json.RawMessage
				if err := json.Unmarshal(row["options"], &options); err != nil {
					return nil, fmt.Errorf("invalid pip parser %s.options: %w", location, err)
				}
				for j, option := range options {
					if _, err := requiredPythonFields(option, fmt.Sprintf("%s.options[%d]", location, j), optionFields...); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	var report pythonParsedReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, fmt.Errorf("invalid pip parser report: %w", err)
	}
	return &report, nil
}

func (f *pythonDependencyFiles) acceptReport(report *pythonParsedReport) (map[string][]byte, error) {
	if report.Schema != 1 || report.PipVersion == "" || report.Root != "requirements.txt" {
		return nil, fmt.Errorf("unrecognized pip parser report schema, pip identity, or root")
	}
	if len(report.Errors) > 0 {
		e := report.Errors[0]
		return nil, fmt.Errorf("pinned pip refused %s:%d: %s: %s", e.Source, e.Line, e.Type, e.Message)
	}
	if len(report.Options) > 0 {
		return nil, fmt.Errorf("unsupported pip option %s in %s", report.Options[0].Name, report.Options[0].Source)
	}
	seenFiles := map[pythonParsedFile]bool{}
	for _, file := range report.Files {
		if !f.roles[file] {
			return nil, fmt.Errorf("pip reported an unaccounted file or include role: %s", file.Path)
		}
		seenFiles[file] = true
	}
	if len(seenFiles) != len(f.roles) {
		return nil, fmt.Errorf("pip parser report is missing followed requirement files")
	}
	seenIncludes := map[pythonParsedInclude]bool{}
	for _, include := range report.Includes {
		if !f.includes[include] {
			return nil, fmt.Errorf("pip reported an unsupported or unaccounted include: %s -> %s", include.Source, include.Value)
		}
		target, err := f.resolve(include.Source, include.Value)
		if err != nil || target != include.Target {
			return nil, fmt.Errorf("pip reported an unsafe include %q: %v", include.Value, err)
		}
		seenIncludes[include] = true
	}
	if len(seenIncludes) != len(f.includes) {
		return nil, fmt.Errorf("pip parser report is missing local includes")
	}
	lines := map[bool][]string{false: {}, true: {}}
	seenRequirements := map[pythonParsedFile]map[int]bool{}
	for _, req := range report.Requirements {
		role := pythonParsedFile{Path: req.Source, Constraint: req.Constraint}
		if !seenFiles[role] || req.Line <= 0 || !f.lines[role][req.Line] {
			return nil, fmt.Errorf("pip requirement has an unaccounted source or line: %s:%d", req.Source, req.Line)
		}
		if req.URL != "" || req.Link != "" || req.Path != "" || req.Archive || req.Editable {
			return nil, fmt.Errorf("only named public-PyPI requirements are supported: %s:%d", req.Source, req.Line)
		}
		if len(req.Options) != 0 {
			return nil, fmt.Errorf("unsupported pip requirement option %s", req.Options[0].Name)
		}
		if !pythonName.MatchString(req.Name) {
			return nil, fmt.Errorf("pip requirement is not a valid named PyPI package")
		}
		extras := append([]string{}, req.Extras...)
		for _, extra := range extras {
			if !pythonName.MatchString(extra) {
				return nil, fmt.Errorf("invalid pip requirement extra %q", extra)
			}
		}
		sort.Strings(extras)
		line := req.Name
		if len(extras) > 0 {
			line += "[" + strings.Join(extras, ",") + "]"
		}
		line += req.Specifier
		if req.Marker != "" {
			line += "; " + req.Marker
		}
		var hashes []string
		for algorithm, values := range req.Hashes {
			if algorithm != "sha256" || len(values) == 0 {
				return nil, fmt.Errorf("unsupported pip hash algorithm or missing values: %s", algorithm)
			}
			for _, value := range values {
				hash := "--hash=" + algorithm + ":" + value
				if !pythonHash.MatchString(hash) {
					return nil, fmt.Errorf("invalid pip requirement hash")
				}
				hashes = append(hashes, hash)
			}
		}
		sort.Strings(hashes)
		for i, hash := range hashes {
			if i == 0 || hashes[i-1] != hash {
				line += " " + hash
			}
		}
		if err := validatePythonRequirement(line); err != nil {
			return nil, fmt.Errorf("unsupported requirement in pip report: %w", err)
		}
		lines[req.Constraint] = append(lines[req.Constraint], line)
		if seenRequirements[role] == nil {
			seenRequirements[role] = map[int]bool{}
		}
		seenRequirements[role][req.Line] = true
	}
	for role, expected := range f.lines {
		if len(seenRequirements[role]) != len(expected) {
			return nil, fmt.Errorf("pip parser report is missing requirements in %s", role.Path)
		}
	}
	// Detect edits made while pip was reading the read-only mount, so evidence
	// cannot silently describe a different original input than the parser saw.
	for filename, original := range f.inputs {
		current, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(filename)))
		if err != nil || !bytes.Equal(current, original) {
			return nil, fmt.Errorf("Python dependency input changed during parsing: %s", filename)
		}
	}
	canonical := map[string][]byte{}
	for _, item := range []struct {
		filename   string
		constraint bool
	}{{"requirements.txt", false}, {"constraints.txt", true}} {
		entries := lines[item.constraint]
		sort.Strings(entries)
		var body strings.Builder
		for i, line := range entries {
			if i == 0 || entries[i-1] != line {
				body.WriteString(line)
				body.WriteByte('\n')
			}
		}
		canonical[item.filename] = []byte(body.String())
	}
	return canonical, nil
}

// Observe pip's actual line parser, recursive includes, option parser, and
// InstallRequirement constructor. In particular, packaging.Requirement alone
// cannot identify pip's separate link or its requirement-option boundary.
const pythonPipParser = `
import json
import os
import optparse
import pip
from pip._internal.network.session import PipSession
from pip._internal.req.req_file import RequirementsFileParser, parse_requirements, handle_requirement_line
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._vendor.packaging.utils import canonicalize_name

report = {"schema": 1, "pip_version": pip.__version__, "root": "requirements.txt",
          "files": [], "includes": [], "requirements": [], "options": [], "errors": []}
root = "/project"
active_source = "requirements.txt"
active_line = 1
original_parse_file = RequirementsFileParser._parse_file
original_option_process = optparse.Option.process

# Observe actual option consumption, including explicit options equal to
# defaults (for example --no-binary :none:). Comparing parsed values with
# defaults would silently omit those forbidden option forms.
def observe_option(self, opt, value, values, parser):
    result = original_option_process(self, opt, value, values, parser)
    rows = getattr(values, "_invariant_options", None)
    if rows is None:
        rows = []
        values._invariant_options = rows
    name = (self._long_opts or self._short_opts)[0]
    rows.append((name, "" if value is None else str(value)))
    return result

optparse.Option.process = observe_option

def relative(filename):
    return os.path.relpath(filename, root)

def input_error(error, source, line):
    report["errors"].append({"source": source, "line": line,
                            "type": type(error).__name__, "message": str(error)})

def no_network(*args, **kwargs):
    raise RuntimeError("the requirements parser cannot access the network")

def option_rows(line, source):
    result = []
    for name, value in getattr(line.opts, "_invariant_options", []):
        if name in ("--requirement", "--constraint", "--editable", "--hash"):
            continue
        result.append({"source": source, "line": line.lineno, "name": name, "value": value})
    return result

def observe_file(self, filename, constraint):
    global active_source, active_line
    source = relative(filename)
    active_source, active_line = source, 1
    report["files"].append({"path": source, "constraint": bool(constraint)})
    try:
        for line in original_parse_file(self, filename, constraint):
            active_source, active_line = source, line.lineno
            options = option_rows(line, source)
            for values, role in ((line.opts.requirements, False), (line.opts.constraints, True)):
                for value in values or []:
                    target = relative(os.path.normpath(os.path.join(os.path.dirname(filename), value)))
                    report["includes"].append({"source": source, "target": target, "value": value, "constraint": role})
            if line.is_requirement:
                try:
                    parsed = handle_requirement_line(line)
                    install = install_req_from_parsed_requirement(parsed, isolated=True)
                    req = install.req
                    link = str(install.link) if install.link else ""
                    local = install.link.file_path if install.link and install.link.is_file else ""
                    report["requirements"].append({
                        "source": source, "line": line.lineno,
                        "name": canonicalize_name(req.name) if req else "",
                        "extras": sorted({canonicalize_name(extra) for extra in req.extras}) if req else [],
                        "specifier": str(req.specifier) if req else "",
                        "marker": str(install.markers) if install.markers else "",
                        "hashes": {algorithm: sorted(set(values)) for algorithm, values in install.hash_options.items()},
                        "constraint": bool(install.constraint), "url": req.url or "" if req else "",
                        "link": link, "path": local,
                        "archive": bool(link and any(link.lower().split("?", 1)[0].endswith(suffix) for suffix in (".whl", ".zip", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz"))),
                        "editable": bool(install.editable), "options": options,
                    })
                except Exception as error:
                    input_error(error, source, line.lineno)
            else:
                report["options"].extend(options)
            yield line
    except Exception as error:
        input_error(error, source, active_line)
        raise

RequirementsFileParser._parse_file = observe_file
try:
    with PipSession() as session:
        session.request = no_network
        for parsed in parse_requirements("/project/requirements.txt", session=session):
            pass
except Exception as error:
    if not report["errors"]:
        input_error(error, active_source, active_line)

for key in ("files", "includes", "requirements", "options", "errors"):
    unique = {json.dumps(row, sort_keys=True): row for row in report[key]}
    report[key] = [unique[key] for key in sorted(unique)]
print(json.dumps(report, sort_keys=True))
`
