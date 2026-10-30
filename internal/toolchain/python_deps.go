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

// PythonDependencies describes the application environment, independently of
// Nagini. Image selects execution only; the remaining fields travel in receipts.
type PythonDependencies struct {
	Image           string           `json:"-"`
	Base            string           `json:"base"`
	Recipe          string           `json:"recipe"`
	Platform        string           `json:"platform"`
	Inputs          []PythonInput    `json:"inputs"`
	CanonicalInputs []PythonInput    `json:"canonical_inputs"`
	Artifacts       []PythonArtifact `json:"artifacts"`
}

type PythonInput struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type PythonArtifact struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant-python-report.json"

// Only generated files enter the build. Neither Python's module search nor
// pip's requirements reader ever sees an application's dependency input files.
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY ["canonical/", "/opt/invariant-canonical/"]
WORKDIR /build
RUN ["/usr/local/bin/python", "-I", "-m", "venv", "/opt/invariant-python"]
RUN ["/opt/invariant-python/bin/python", "-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-input", "--no-cache-dir", "--ignore-installed", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--report", "` + pythonDepsReport + `", "--requirement", "/opt/invariant-canonical/requirements.txt", "--constraint", "/opt/invariant-canonical/constraints.txt"]
ENV PATH="/opt/invariant-python/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin"
`

type pythonDepsInputs struct {
	environment PythonDependencies
	canonical   map[string][]byte
}

func pythonDepsHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PythonDepsRecipe validates the complete input closure without running Docker.
// A missing root requirements.txt keeps the standard-library-only path.
func PythonDepsRecipe(root string) (string, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.environment.Recipe, nil
}

// PythonDepsImage builds or reuses the recipe, then reads that image's actual
// resolution offline. An installation or evidence failure never selects a
// different environment. The tag is only a cache key, never an execution target.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.environment
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	id, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if inspectErr != nil {
		work, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
		dir := filepath.Join(work, "context")
		if err := os.MkdirAll(filepath.Join(dir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		for name, b := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(dir, "canonical", name), b, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		iidfile := filepath.Join(work, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies: %w\n%s", err, out)
		}
		id, err = os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("reading the Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(id))
	if !pythonDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("Python dependencies: Docker returned no immutable image ID")
	}
	out, err := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", env.Image, "/bin/cat", pythonDepsReport).Output()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation evidence from %s: %w", env.Image, err)
	}
	env.Artifacts, err = pythonArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("Python dependency installation evidence: %w", err)
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
	type visit struct {
		path       string
		constraint bool
	}
	raw := map[string][]byte{}
	active, done := map[string]bool{}, map[visit]bool{}
	canonical := map[string][]byte{"requirements.txt": {}, "constraints.txt": {}}
	var read func(string, string, bool) error
	read = func(containing, reference string, constraint bool) error {
		rel, err := pythonInputPath(root, containing, reference)
		if err != nil {
			return err
		}
		if active[rel] {
			return fmt.Errorf("Python dependency include cycle at %s", rel)
		}
		key := visit{rel, constraint}
		if done[key] {
			return nil
		}
		b, ok := raw[rel]
		if !ok {
			b, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				return err
			}
			raw[rel] = b
		}
		lines, err := pythonDependencyLines(b)
		if err != nil {
			return fmt.Errorf("Python dependencies in %s: %w", rel, err)
		}
		active[rel] = true
		defer delete(active, rel)
		for i, line := range lines {
			if strings.HasPrefix(line, "-") {
				m := pythonInclude.FindStringSubmatch(line)
				if m == nil {
					return fmt.Errorf("Python dependencies in %s: unsupported include or option %q", rel, line)
				}
				if err := read(rel, m[3], m[1] == "-c" || m[2] == "--constraint"); err != nil {
					return fmt.Errorf("Python dependencies referenced from %s: %w", rel, err)
				}
				continue
			}
			requirement, err := parsePythonRequirement(line)
			if err != nil {
				return fmt.Errorf("Python dependencies in %s, entry %d: %w", rel, i+1, err)
			}
			name := "requirements.txt"
			if constraint {
				name = "constraints.txt"
			}
			canonical[name] = append(canonical[name], requirement+"\n"...)
		}
		done[key] = true
		return nil
	}
	if err := read("requirements.txt", "requirements.txt", false); err != nil {
		return nil, err
	}
	inputs := &pythonDepsInputs{
		environment: PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform},
		canonical:   canonical,
	}
	h := sha256.New()
	write := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	for _, s := range []string{PythonImage, pythonDepsPlatform, pythonDepsDockerfile, "original inputs"} {
		write([]byte(s))
	}
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	sort.Strings(names)
	write([]byte(fmt.Sprint(len(names))))
	for _, name := range names {
		write([]byte(name))
		write(raw[name])
		inputs.environment.Inputs = append(inputs.environment.Inputs, PythonInput{name, pythonDepsHash(raw[name])})
	}
	write([]byte("canonical inputs"))
	for _, name := range []string{"constraints.txt", "requirements.txt"} {
		write([]byte("canonical/" + name))
		write(canonical[name])
		inputs.environment.CanonicalInputs = append(inputs.environment.CanonicalInputs, PythonInput{name, pythonDepsHash(canonical[name])})
	}
	inputs.environment.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return inputs, nil
}

// Walk each component before cleaning: a symlink or an escape remains unsafe
// even when a later '..' would cancel it. Filenames are read literally.
func pythonInputPath(root, containing, reference string) (string, error) {
	if !pythonLocalPath.MatchString(reference) || path.IsAbs(reference) || strings.HasPrefix(reference, "-") {
		return "", fmt.Errorf("unsupported Python dependency path %q", reference)
	}
	current := filepath.Join(root, filepath.Dir(filepath.FromSlash(containing)))
	parts := strings.Split(reference, "/")
	for i, part := range parts {
		if part == ".." {
			if current == root {
				return "", fmt.Errorf("Python dependency path %q leaves the project", reference)
			}
			current = filepath.Dir(current)
		} else {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("Python dependency path %q: %w", reference, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("Python dependency path %q must traverse directories and end in a regular file, without symlinks", reference)
		}
	}
	rel, err := filepath.Rel(root, current)
	return filepath.ToSlash(rel), err
}

var (
	pythonDigest    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonLocalPath = regexp.MustCompile(`^[A-Za-z0-9_./\[\]-]+$`)
	pythonInclude   = regexp.MustCompile(`^(?:(-r|-c)[ \t]*|(--requirement|--constraint)(?:=|[ \t]+))([A-Za-z0-9_./\[\]-]+)$`)
	pythonEncoding  = regexp.MustCompile(`(?i)coding[ \t]*[:=]`)
)

func pythonDependencyLines(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	for i, c := range b {
		if c == '\r' && (i+1 == len(b) || b[i+1] != '\n') {
			return nil, fmt.Errorf("bare CR is unsupported; use LF or CRLF")
		}
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	if strings.ContainsAny(text, "\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff") || pythonEncoding.MatchString(text) {
		return nil, fmt.Errorf("encoding declarations and non-LF/CRLF line separators are unsupported")
	}
	for _, r := range text {
		if (r < 32 && r != '\t' && r != '\r' && r != '\n') || r == 127 {
			return nil, fmt.Errorf("unsupported control character in requirements")
		}
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var lines []string
	pending := ""
	continued := false
	physical := strings.Split(text, "\n")
	for i, line := range physical {
		if i == len(physical)-1 && line == "" {
			break
		}
		n := strings.Count(line, "\\")
		continuation := n == 1 && strings.HasSuffix(line, "\\")
		if n != 0 && !continuation {
			return nil, fmt.Errorf("line %d: only a single trailing backslash continuation is supported", i+1)
		}
		if continuation {
			if i == len(physical)-1 {
				return nil, fmt.Errorf("line %d: dangling continuation", i+1)
			}
			line = strings.TrimSuffix(line, "\\")
			clean, err := pythonDependencyComment(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			if clean != strings.Trim(line, " \t") {
				return nil, fmt.Errorf("line %d: a backslash in a comment is not a continuation", i+1)
			}
		}
		pending += line
		continued = continuation
		if continuation {
			pending += " "
			continue
		}
		clean, err := pythonDependencyComment(pending)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if clean != "" {
			lines = append(lines, clean)
		}
		pending = ""
	}
	if continued {
		return nil, fmt.Errorf("dangling continuation")
	}
	return lines, nil
}

func pythonDependencyComment(line string) (string, error) {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if c == '#' {
			if quote != 0 || (i > 0 && line[i-1] != ' ' && line[i-1] != '\t') {
				return "", fmt.Errorf("unsupported '#' in a requirement or marker")
			}
			return strings.Trim(line[:i], " \t"), nil
		}
		if c == quote {
			quote = 0
		} else if quote == 0 && (c == '\'' || c == '"') {
			quote = c
		}
	}
	return strings.Trim(line, " \t"), nil
}

var (
	pythonName        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonNamePrefix  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)
	pythonVersion     = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*(?:[-_.]?(?:alpha|beta|preview|pre|a|b|rc)[-_.]?[0-9]*)?(?:-[0-9]+|[-_.]?(?:post|rev|r)[-_.]?[0-9]*)?(?:[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)
	pythonWildcard    = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*\.\*$`)
	pythonCompatible  = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+\.[0-9]+`)
	pythonVersionAtom = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.!+*_-]*`)
	pythonHash        = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	pythonNormalize   = regexp.MustCompile(`[-_.]+`)
)

// This parser consumes the whole supported grammar, then serializes its
// tokens. No unparsed suffix or quoted pip option reaches the installer.
type pythonRequirementParser struct{ rest string }

func (p *pythonRequirementParser) space() {
	p.rest = strings.TrimLeft(p.rest, " \t")
}

func (p *pythonRequirementParser) take(token string) bool {
	rest := strings.TrimLeft(p.rest, " \t")
	if !strings.HasPrefix(rest, token) {
		return false
	}
	p.rest = rest[len(token):]
	return true
}

func (p *pythonRequirementParser) word(token string) bool {
	rest := strings.TrimLeft(p.rest, " \t")
	if !strings.HasPrefix(rest, token) {
		return false
	}
	end := len(token)
	if end < len(rest) && strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_", rune(rest[end])) {
		return false
	}
	p.rest = rest[end:]
	return true
}

func (p *pythonRequirementParser) name() string {
	p.space()
	name := pythonNamePrefix.FindString(p.rest)
	if !pythonName.MatchString(name) {
		return ""
	}
	p.rest = p.rest[len(name):]
	return name
}

func (p *pythonRequirementParser) operator() string {
	for _, op := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
		if p.take(op) {
			return op
		}
	}
	return ""
}

func parsePythonRequirement(line string) (string, error) {
	p := pythonRequirementParser{rest: line}
	name := p.name()
	if name == "" {
		return "", fmt.Errorf("expected a PyPI wheel requirement, got %q", line)
	}
	for _, suffix := range []string{".whl", ".zip", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return "", fmt.Errorf("local archives are unsupported: %q", name)
		}
	}
	out := name
	if p.take("[") {
		var extras []string
		for {
			extra := p.name()
			if extra == "" {
				return "", fmt.Errorf("invalid extras in %q", line)
			}
			extras = append(extras, extra)
			if !p.take(",") {
				break
			}
		}
		if !p.take("]") {
			return "", fmt.Errorf("unterminated extras in %q", line)
		}
		out += "[" + strings.Join(extras, ",") + "]"
	}
	parenthesized := p.take("(")
	var versions []string
	for {
		op := p.operator()
		if op == "" {
			if len(versions) != 0 || parenthesized {
				return "", fmt.Errorf("expected a version comparison in %q", line)
			}
			break
		}
		p.space()
		version := pythonVersionAtom.FindString(p.rest)
		valid := pythonVersion.MatchString(version)
		switch op {
		case "===":
			valid = version != "" && !strings.Contains(version, "*")
		case "==", "!=":
			valid = valid || pythonWildcard.MatchString(version)
		default:
			valid = valid && !strings.Contains(version, "+")
			if op == "~=" {
				valid = valid && pythonCompatible.MatchString(version)
			}
		}
		if !valid {
			return "", fmt.Errorf("invalid version in %q", line)
		}
		p.rest = p.rest[len(version):]
		versions = append(versions, op+version)
		if !p.take(",") {
			break
		}
	}
	if parenthesized && !p.take(")") {
		return "", fmt.Errorf("unterminated version comparisons in %q", line)
	}
	out += strings.Join(versions, ",")
	if p.take(";") {
		marker, ok := p.markerOr()
		if !ok {
			return "", fmt.Errorf("invalid or unsafe PEP 508 marker in %q", line)
		}
		out += "; " + marker
	}
	for strings.Trim(p.rest, " \t") != "" {
		if (p.rest[0] != ' ' && p.rest[0] != '\t') || !p.take("--hash=sha256:") {
			return "", fmt.Errorf("unsupported requirement syntax or option in %q", line)
		}
		end := strings.IndexAny(p.rest, " \t")
		if end < 0 {
			end = len(p.rest)
		}
		hash := p.rest[:end]
		if !pythonHash.MatchString(hash) {
			return "", fmt.Errorf("expected a complete SHA-256 hash in %q", line)
		}
		out += " --hash=sha256:" + strings.ToLower(hash)
		p.rest = p.rest[end:]
	}
	return out, nil
}

func (p *pythonRequirementParser) markerOr() (string, bool) {
	left, ok := p.markerAnd()
	for ok && p.word("or") {
		var right string
		right, ok = p.markerAnd()
		left += " or " + right
	}
	return left, ok
}

func (p *pythonRequirementParser) markerAnd() (string, bool) {
	left, ok := p.markerAtom()
	for ok && p.word("and") {
		var right string
		right, ok = p.markerAtom()
		left += " and " + right
	}
	return left, ok
}

func (p *pythonRequirementParser) markerAtom() (string, bool) {
	if p.take("(") {
		inner, ok := p.markerOr()
		return "(" + inner + ")", ok && p.take(")")
	}
	left, ok := p.markerValue()
	if !ok {
		return "", false
	}
	op := p.operator()
	if op == "" {
		if p.word("in") {
			op = "in"
		} else if p.word("not") && p.word("in") {
			op = "not in"
		} else {
			return "", false
		}
	}
	right, ok := p.markerValue()
	return left + " " + op + " " + right, ok
}

func (p *pythonRequirementParser) markerValue() (string, bool) {
	p.space()
	if len(p.rest) == 0 {
		return "", false
	}
	if quote := p.rest[0]; quote == '\'' || quote == '"' {
		end := strings.IndexByte(p.rest[1:], quote)
		if end < 0 {
			return "", false
		}
		value := p.rest[1 : end+1]
		for _, c := range value {
			if c < 32 || c > 126 || strings.ContainsRune("#$`\\", c) {
				return "", false
			}
		}
		// pip splits option tokens even inside marker strings. Keep these
		// outside the grammar, including otherwise harmless-looking hashes.
		for _, token := range strings.Fields(value) {
			if strings.HasPrefix(token, "-") {
				return "", false
			}
		}
		literal := p.rest[:end+2]
		p.rest = p.rest[end+2:]
		return literal, true
	}
	name := p.name()
	switch name {
	case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release",
		"platform_system", "platform_version", "platform_machine", "platform_python_implementation",
		"implementation_name", "implementation_version", "extra":
		return name, true
	}
	return "", false
}

func pythonArtifacts(b []byte) ([]PythonArtifact, error) {
	var report struct {
		Version string `json:"version"`
		Install []struct {
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
		} `json:"install"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, fmt.Errorf("invalid pip report: %w", err)
	}
	if report.Version != "1" || report.Install == nil {
		return nil, fmt.Errorf("missing pip version-1 installation report")
	}
	artifacts := make([]PythonArtifact, 0, len(report.Install))
	seen := map[string]bool{}
	for _, installed := range report.Install {
		name, version := installed.Metadata.Name, installed.Metadata.Version
		u, err := url.Parse(installed.DownloadInfo.URL)
		if err != nil || u.Scheme == "" {
			return nil, fmt.Errorf("missing wheel URL for %q", name)
		}
		filename := path.Base(u.Path)
		parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
		hash := installed.DownloadInfo.ArchiveInfo.Hashes["sha256"]
		if !pythonName.MatchString(name) || !pythonVersion.MatchString(version) || !pythonHash.MatchString(hash) ||
			!strings.HasSuffix(filename, ".whl") || (len(parts) != 5 && len(parts) != 6) {
			return nil, fmt.Errorf("incomplete resolved wheel name, version, filename or SHA-256 for %q", name)
		}
		for _, part := range parts {
			if !pythonVersionAtom.MatchString(part) || pythonVersionAtom.FindString(part) != part {
				return nil, fmt.Errorf("invalid wheel filename %q", filename)
			}
		}
		name = pythonNormalize.ReplaceAllString(strings.ToLower(name), "-")
		if pythonNormalize.ReplaceAllString(strings.ToLower(parts[0]), "-") != name || seen[name] {
			return nil, fmt.Errorf("inconsistent or duplicate wheel evidence for %q", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonArtifact{name, version, filename, "sha256:" + strings.ToLower(hash)})
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
