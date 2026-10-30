package toolchain

import (
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
	"unicode/utf8"
)

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant/python-report.json"

// Only generated files enter the build. Neither interpreter can import from
// the input directory, and pip receives no application-controlled options.
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY ["canonical/", "/opt/invariant/canonical/"]
WORKDIR /opt/invariant/build
ENV PIP_CONFIG_FILE=/dev/null
RUN ["/usr/local/bin/python", "-I", "-m", "venv", "/opt/invariant/venv"]
RUN ["/opt/invariant/venv/bin/python", "-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-cache-dir", "--ignore-installed", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--report", "` + pythonDepsReport + `", "-r", "/opt/invariant/canonical/requirements.txt", "-c", "/opt/invariant/canonical/constraints.txt"]
ENV PATH=/opt/invariant/venv/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
`

// PythonDependencies separates reusable environment evidence from the local
// immutable image used for execution. A recipe can have different resolutions
// after cache eviction when its requirements are not fully pinned.
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

type pythonDepsInputs struct {
	original  map[string][]byte
	canonical map[string][]byte
	evidence  PythonDependencies
}

func pythonDepsHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PythonDepsRecipe validates and identifies the complete dependency closure
// without invoking Docker. An absent root requirements.txt needs no image.
func PythonDepsRecipe(root string) (string, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.evidence.Recipe, nil
}

// PythonDepsImage builds or reuses a wheel-only application environment. It
// reads the saved installation report from the selected immutable image on
// every call; failed installation or evidence never selects a fallback.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.evidence
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	out, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{json .}}", tag).Output()
	if inspectErr == nil {
		var selected struct{ Id, Os, Architecture string }
		if err := json.Unmarshal(out, &selected); err != nil || !pythonDigest.MatchString(selected.Id) || selected.Os+"/"+selected.Architecture != pythonDepsPlatform {
			return nil, fmt.Errorf("Python dependencies: invalid image identity or platform for %s", tag)
		}
		env.Image = selected.Id
	} else {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		contextDir := filepath.Join(dir, "context")
		if err := os.MkdirAll(filepath.Join(contextDir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		for name, body := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(contextDir, "canonical", name), body, 0o644); err != nil {
				return nil, err
			}
		}
		iidfile := filepath.Join(dir, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, contextDir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python dependencies from requirements.txt: %w\n%s", err, out)
		}
		id, err := os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("Python dependencies: reading built image identity: %w", err)
		}
		env.Image = strings.TrimSpace(string(id))
		if !pythonDigest.MatchString(env.Image) {
			return nil, fmt.Errorf("Python dependencies: build returned an invalid image identity")
		}
	}
	out, err = Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", env.Image, "/bin/cat", pythonDepsReport).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("Python dependencies: reading installed wheel evidence from %s: %w\n%s", env.Image, err, out)
	}
	env.Artifacts, err = pythonArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("Python dependencies: invalid installed wheel evidence from %s: %w", env.Image, err)
	}
	return &env, nil
}

func readPythonDeps(root string) (*pythonDepsInputs, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	inputs := &pythonDepsInputs{original: map[string][]byte{}, canonical: map[string][]byte{}}
	active, visited := map[string]bool{}, map[string]bool{}
	var requirements, constraints strings.Builder
	var visit func(string, string, bool) error
	visit = func(from, reference string, constraint bool) error {
		name, err := pythonInputPath(root, from, reference)
		if err != nil {
			return err
		}
		if active[name] {
			return fmt.Errorf("dependency reference cycle at %s", name)
		}
		key := fmt.Sprintf("%t:%s", constraint, name)
		if visited[key] {
			return nil
		}
		body, ok := inputs.original[name]
		if !ok {
			body, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				return err
			}
			inputs.original[name] = body
		}
		lines, err := pythonRequirementLines(body)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		active[name] = true
		defer delete(active, name)
		for _, line := range lines {
			if strings.HasPrefix(line, "-") {
				ref, role, err := pythonInclude(line)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				if err := visit(name, ref, role); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				continue
			}
			parsed, err := parsePythonRequirement(line)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if constraint {
				constraints.WriteString(parsed + "\n")
			} else {
				requirements.WriteString(parsed + "\n")
			}
		}
		visited[key] = true
		return nil
	}
	if err := visit("", "requirements.txt", false); err != nil {
		return nil, fmt.Errorf("Python dependencies: %w", err)
	}
	inputs.canonical["requirements.txt"] = []byte(requirements.String())
	inputs.canonical["constraints.txt"] = []byte(constraints.String())
	inputs.evidence.Base, inputs.evidence.Platform = PythonImage, pythonDepsPlatform
	h := sha256.New()
	write := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	for _, value := range []string{"python-dependencies-v1", PythonImage, pythonDepsPlatform, pythonDepsDockerfile} {
		write([]byte(value))
	}
	for _, group := range []struct {
		name     string
		files    map[string][]byte
		evidence *[]PythonDependencyInput
	}{
		{"original", inputs.original, &inputs.evidence.Inputs},
		{"canonical", inputs.canonical, &inputs.evidence.CanonicalInputs},
	} {
		write([]byte(group.name))
		var names []string
		for name := range group.files {
			names = append(names, name)
		}
		sort.Strings(names)
		write([]byte(fmt.Sprint(len(names))))
		for _, name := range names {
			body := group.files[name]
			write([]byte(name))
			write(body)
			*group.evidence = append(*group.evidence, PythonDependencyInput{Path: name, SHA256: pythonDepsHash(body)})
		}
	}
	inputs.evidence.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return inputs, nil
}

// Inspect each traversed component before cleaning '..': even a canceled
// directory must exist, be inside the root, and not be a symbolic link.
func pythonInputPath(root, from, reference string) (string, error) {
	if !pythonLocalPath.MatchString(reference) || path.IsAbs(reference) {
		return "", fmt.Errorf("unsupported local dependency reference %q", reference)
	}
	var parts []string
	if from != "" && path.Dir(from) != "." {
		parts = strings.Split(path.Dir(from), "/")
	}
	traversed := strings.Split(reference, "/")
	for i, part := range traversed {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("dependency reference %q escapes the project", reference)
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(strings.Join(parts, "/"))))
		if err != nil {
			return "", fmt.Errorf("dependency reference %q: %w", reference, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dependency reference %q traverses a symbolic link", reference)
		}
		last := i == len(traversed)-1
		if (!last && !info.IsDir()) || (last && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("dependency reference %q must traverse directories to a regular file", reference)
		}
	}
	if len(parts) == 0 || strings.HasSuffix(reference, "/") || path.Base(reference) == "." || path.Base(reference) == ".." {
		return "", fmt.Errorf("dependency reference %q does not name a regular file", reference)
	}
	return strings.Join(parts, "/"), nil
}

var (
	pythonDigest     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonHash       = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	pythonLocalPath  = regexp.MustCompile(`^[A-Za-z0-9._/\[\]-]+$`)
	pythonEncoding   = regexp.MustCompile(`coding[:=][ \t]*[-A-Za-z0-9_.]+`)
	pythonName       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonVersion    = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*(?:[-_.]?(?:a|b|c|rc|alpha|beta|pre|preview)[-_.]?[0-9]*)?(?:-[0-9]+|[-_.]?(?:post|rev|r)[-_.]?[0-9]*)?(?:[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)
	pythonWildcard   = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*\.\*$`)
	pythonCompatible = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+\.[0-9]+`)
)

// Decode only UTF-8, then perform pip's single-backslash join without trimming
// physical lines or inserting spaces. Comment syntax is interpreted afterward.
func pythonRequirementLines(body []byte) ([]string, error) {
	if !utf8.Valid(body) {
		return nil, fmt.Errorf("requirements must be valid UTF-8")
	}
	text := strings.TrimPrefix(string(body), "\ufeff")
	for i, r := range text {
		if r == '\r' && (i+1 == len(text) || text[i+1] != '\n') {
			return nil, fmt.Errorf("bare CR is not a supported line ending")
		}
		if strings.ContainsRune("\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff", r) || (r < 32 && r != '\t' && r != '\n' && r != '\r') {
			return nil, fmt.Errorf("unsupported character U+%04X in requirements", r)
		}
	}
	physical := strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	var lines []string
	var pending string
	for i, line := range physical {
		if comment := strings.IndexByte(line, '#'); comment >= 0 && pythonEncoding.MatchString(line[comment:]) {
			return nil, fmt.Errorf("line %d: encoding declarations in comments are not supported", i+1)
		}
		if slash := strings.IndexByte(line, '\\'); slash >= 0 {
			if slash != len(line)-1 || strings.HasPrefix(strings.TrimLeft(line, " \t"), "#") {
				return nil, fmt.Errorf("line %d: only a single trailing continuation backslash is supported", i+1)
			}
			if i == len(physical)-1 {
				return nil, fmt.Errorf("line %d: dangling continuation", i+1)
			}
			pending += line[:len(line)-1]
			continue
		}
		logical, err := pythonStripComment(pending + line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		pending = ""
		if logical != "" {
			lines = append(lines, logical)
		}
	}
	return lines, nil
}

func pythonStripComment(line string) (string, error) {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '#' {
			if quote != 0 {
				return "", fmt.Errorf("marker literals containing # are not supported")
			}
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				if pythonEncoding.MatchString(line[i:]) {
					return "", fmt.Errorf("encoding declarations in comments are not supported")
				}
				return strings.Trim(line[:i], " \t"), nil
			}
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
		} else if c == '\'' || c == '"' {
			quote = c
		}
	}
	return strings.Trim(line, " \t"), nil
}

func pythonInclude(line string) (string, bool, error) {
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		rest := line[len(option):]
		if len(option) > 2 {
			if strings.HasPrefix(rest, "=") {
				rest = rest[1:]
			} else if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
				break
			}
		}
		rest = strings.Trim(rest, " \t")
		if !pythonLocalPath.MatchString(rest) {
			break
		}
		return rest, option == "-c" || option == "--constraint", nil
	}
	return "", false, fmt.Errorf("unsupported dependency include or option %q", line)
}

type pythonRequirementParser struct{ rest string }

func (p *pythonRequirementParser) space() { p.rest = strings.TrimLeft(p.rest, " \t") }

func (p *pythonRequirementParser) take(token string) bool {
	p.space()
	if !strings.HasPrefix(p.rest, token) {
		return false
	}
	p.rest = p.rest[len(token):]
	return true
}

func (p *pythonRequirementParser) word(word string) bool {
	p.space()
	if !strings.HasPrefix(p.rest, word) {
		return false
	}
	if len(p.rest) > len(word) && pythonNameChar(p.rest[len(word)]) {
		return false
	}
	p.rest = p.rest[len(word):]
	return true
}

func pythonNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", rune(c))
}

func (p *pythonRequirementParser) name() string {
	p.space()
	i := 0
	for i < len(p.rest) && pythonNameChar(p.rest[i]) {
		i++
	}
	name := p.rest[:i]
	if !pythonName.MatchString(name) {
		return ""
	}
	p.rest = p.rest[i:]
	return name
}

func parsePythonRequirement(line string) (string, error) {
	// pip splits requirement arguments from options at whitespace. A glued
	// hash must not become valid merely because serialization adds a space.
	var quote byte
	for i := 0; i < len(line); i++ {
		if quote != 0 {
			if line[i] == quote {
				quote = 0
			}
		} else if line[i] == '\'' || line[i] == '"' {
			quote = line[i]
		} else if strings.HasPrefix(line[i:], "--hash=") && (i == 0 || line[i-1] != ' ' && line[i-1] != '\t') {
			return "", fmt.Errorf("unsupported requirement %q: hashes need a whitespace separator", line)
		}
	}
	p := pythonRequirementParser{rest: line}
	result, err := p.requirement()
	if err != nil {
		return "", fmt.Errorf("unsupported requirement %q: %w", line, err)
	}
	return result, nil
}

func (p *pythonRequirementParser) requirement() (string, error) {
	name := p.name()
	if name == "" {
		return "", fmt.Errorf("expected a PyPI package name")
	}
	// These spellings can be interpreted as local archives by pip even
	// without a slash. They are outside the named-wheel requirement grammar.
	for _, suffix := range []string{".whl", ".zip", ".tar", ".gz", ".bz2", ".xz", ".lz", ".lzma", ".tgz", ".tbz", ".tbz2", ".txz", ".tlz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return "", fmt.Errorf("local or source archives are not supported")
		}
	}
	if p.take("[") {
		var extras []string
		for {
			extra := p.name()
			if extra == "" {
				return "", fmt.Errorf("expected an extra name")
			}
			extras = append(extras, extra)
			if !p.take(",") {
				break
			}
		}
		if !p.take("]") {
			return "", fmt.Errorf("expected closing extras bracket")
		}
		name += "[" + strings.Join(extras, ",") + "]"
	}
	parenthesized := p.take("(")
	var versions []string
	for {
		op := p.comparison()
		if op == "" {
			if len(versions) != 0 || parenthesized {
				return "", fmt.Errorf("expected a version specifier")
			}
			break
		}
		p.space()
		i := strings.IndexAny(p.rest, " \t,;)")
		if i < 0 {
			i = len(p.rest)
		}
		version := p.rest[:i]
		valid := pythonVersion.MatchString(version)
		if op == "==" || op == "!=" {
			valid = valid || pythonWildcard.MatchString(version)
		} else if strings.Contains(version, "+") {
			valid = false
		}
		if op == "~=" && !pythonCompatible.MatchString(version) {
			valid = false
		}
		if !valid {
			return "", fmt.Errorf("invalid version %q for %s", version, op)
		}
		p.rest = p.rest[i:]
		versions = append(versions, op+version)
		if !p.take(",") {
			break
		}
	}
	if parenthesized && !p.take(")") {
		return "", fmt.Errorf("expected closing version parenthesis")
	}
	name += strings.Join(versions, ",")
	if p.take(";") {
		marker, err := p.markerOr()
		if err != nil {
			return "", err
		}
		name += "; " + marker
	}
	for {
		p.space()
		if p.rest == "" {
			return name, nil
		}
		if !p.take("--hash=sha256:") || len(p.rest) < 64 || !pythonDigest.MatchString("sha256:"+p.rest[:64]) {
			return "", fmt.Errorf("expected only SHA-256 hashes after the requirement")
		}
		name += " --hash=sha256:" + p.rest[:64]
		p.rest = p.rest[64:]
		if p.rest != "" && p.rest[0] != ' ' && p.rest[0] != '\t' {
			return "", fmt.Errorf("unparsed hash suffix")
		}
	}
}

func (p *pythonRequirementParser) comparison() string {
	for _, op := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
		if p.take(op) {
			return op
		}
	}
	return ""
}

func (p *pythonRequirementParser) markerOr() (string, error) {
	left, err := p.markerAnd()
	for err == nil && p.word("or") {
		var right string
		right, err = p.markerAnd()
		left += " or " + right
	}
	return left, err
}

func (p *pythonRequirementParser) markerAnd() (string, error) {
	left, err := p.markerAtom()
	for err == nil && p.word("and") {
		var right string
		right, err = p.markerAtom()
		left += " and " + right
	}
	return left, err
}

func (p *pythonRequirementParser) markerAtom() (string, error) {
	if p.take("(") {
		value, err := p.markerOr()
		if err != nil {
			return "", err
		}
		if !p.take(")") {
			return "", fmt.Errorf("expected closing marker parenthesis")
		}
		return "(" + value + ")", nil
	}
	left, err := p.markerValue()
	if err != nil {
		return "", err
	}
	op := p.comparison()
	if op == "" {
		if p.word("in") {
			op = "in"
		} else if p.word("not") && p.word("in") {
			op = "not in"
		}
	}
	if op == "" {
		return "", fmt.Errorf("expected a marker comparison")
	}
	right, err := p.markerValue()
	return left + " " + op + " " + right, err
}

func (p *pythonRequirementParser) markerValue() (string, error) {
	p.space()
	if len(p.rest) > 0 && (p.rest[0] == '\'' || p.rest[0] == '"') {
		end := strings.IndexByte(p.rest[1:], p.rest[0])
		if end < 0 {
			return "", fmt.Errorf("unterminated marker literal")
		}
		literal := p.rest[:end+2]
		for _, c := range literal[1 : len(literal)-1] {
			if c < 32 || c > 126 || strings.ContainsRune("#\\$`", c) {
				return "", fmt.Errorf("unsupported marker literal")
			}
		}
		for _, word := range strings.Fields(literal[1 : len(literal)-1]) {
			if strings.HasPrefix(word, "-") {
				return "", fmt.Errorf("option-like marker literals are not supported")
			}
		}
		p.rest = p.rest[len(literal):]
		return literal, nil
	}
	name := p.name()
	switch name {
	case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra":
		return name, nil
	default:
		return "", fmt.Errorf("unknown marker variable %q", name)
	}
}

func pythonArtifacts(body []byte) ([]PythonArtifact, error) {
	var report struct {
		Version    string `json:"version"`
		PipVersion string `json:"pip_version"`
		Install    *[]struct {
			Metadata struct{ Name, Version string } `json:"metadata"`
			Download struct {
				URL     string `json:"url"`
				Archive struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
		} `json:"install"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		return nil, err
	}
	if report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("missing or unsupported pip installation report")
	}
	artifacts := make([]PythonArtifact, 0, len(*report.Install))
	seen := map[string]bool{}
	for _, installed := range *report.Install {
		name, version := installed.Metadata.Name, installed.Metadata.Version
		if !pythonName.MatchString(name) || !pythonVersion.MatchString(version) {
			return nil, fmt.Errorf("missing or invalid distribution name or version")
		}
		name = pythonNormalizedName(name)
		location, err := url.Parse(installed.Download.URL)
		if err != nil || location.Scheme == "" {
			return nil, fmt.Errorf("%s: missing or invalid wheel URL", name)
		}
		filename := path.Base(location.Path)
		parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
		if !strings.HasSuffix(filename, ".whl") || (len(parts) != 5 && len(parts) != 6) || pythonNormalizedName(parts[0]) != name || parts[1] != version {
			return nil, fmt.Errorf("%s: invalid wheel filename %q", name, filename)
		}
		hash := installed.Download.Archive.Hashes["sha256"]
		if !pythonHash.MatchString(hash) {
			return nil, fmt.Errorf("%s: missing or invalid wheel SHA-256", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate installed distribution %s", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonArtifact{Name: name, Version: version, Filename: filename, SHA256: "sha256:" + strings.ToLower(hash)})
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

func pythonNormalizedName(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if c == '-' || c == '_' || c == '.' {
			if b.Len() == 0 || b.String()[b.Len()-1] != '-' {
				b.WriteByte('-')
			}
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
