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
	"unicode"
	"unicode/utf8"
)

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant/report.json"

// Only generated requirements reach pip. Neither Python module discovery nor
// Docker's COPY pattern matching ever sees an application's input filenames.
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY ["canonical/", "/opt/invariant/canonical/"]
WORKDIR /opt/invariant/build
RUN ["/usr/local/bin/python", "-I", "-m", "venv", "/opt/invariant/venv"]
RUN ["/opt/invariant/venv/bin/python", "-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-cache-dir", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--ignore-installed", "--report", "` + pythonDepsReport + `", "-r", "/opt/invariant/canonical/requirements.txt", "-c", "/opt/invariant/canonical/constraints.txt"]
ENV PATH="/opt/invariant/venv/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin"
`

// PythonDependencies describes the environment actually selected, including
// floating requirements' resolution. Image is for execution, not fingerprints.
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

type pythonDependencyFiles struct {
	root      string
	original  map[string][]byte
	canonical map[string][]byte
	active    map[string]bool
	visited   map[string]bool
}

func pythonDependencyHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// PythonDepsRecipe validates the complete input closure without invoking Docker.
// An absent requirements.txt preserves the standard-library execution path.
func PythonDepsRecipe(root string) (string, error) {
	files, err := readPythonDependencies(root)
	if err != nil || files == nil {
		return "", err
	}
	return files.evidence().Recipe, nil
}

// PythonDepsImage builds or reuses the recipe, then reads its actual pip report
// using the same immutable image ID that the caller must use for execution.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	files, err := readPythonDependencies(root)
	if err != nil || files == nil {
		return nil, err
	}
	env := files.evidence()
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	out, err := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if err != nil {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		if err := os.Mkdir(filepath.Join(dir, "canonical"), 0o755); err != nil {
			return nil, err
		}
		for name, b := range files.canonical {
			if err := os.WriteFile(filepath.Join(dir, "canonical", name), b, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		// The ID file stays outside the context, so only the recipe and its
		// two canonical inputs are visible to the builder.
		idDir, err := os.MkdirTemp("", "invariant-python-image-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(idDir)
		idFile := filepath.Join(idDir, "id")
		log, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", idFile, "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies: %w\n%s", err, log)
		}
		out, err = os.ReadFile(idFile)
		if err != nil {
			return nil, fmt.Errorf("reading Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(out))
	if !pythonDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("invalid Python dependency image ID %q", env.Image)
	}
	out, err = Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", env.Image, "/bin/cat", pythonDepsReport).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation report: %w\n%s", err, out)
	}
	env.Artifacts, err = pythonDependencyArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("Python dependency installation report: %w", err)
	}
	return env, nil
}

func readPythonDependencies(root string) (*pythonDependencyFiles, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	f := &pythonDependencyFiles{
		root: root, original: map[string][]byte{},
		canonical: map[string][]byte{"requirements.txt": {}, "constraints.txt": {}},
		active:    map[string]bool{}, visited: map[string]bool{},
	}
	if err := f.visit(".", "requirements.txt", false); err != nil {
		return nil, fmt.Errorf("Python dependencies: %w", err)
	}
	return f, nil
}

func (f *pythonDependencyFiles) evidence() *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform}
	h := sha256.New()
	add := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	add([]byte(pythonDepsDockerfile))
	add([]byte(PythonImage))
	add([]byte(pythonDepsPlatform))
	for _, group := range []struct {
		name   string
		files  map[string][]byte
		inputs *[]PythonDependencyInput
	}{
		{"original", f.original, &env.Inputs},
		{"canonical", f.canonical, &env.CanonicalInputs},
	} {
		add([]byte(group.name))
		var names []string
		for name := range group.files {
			names = append(names, name)
		}
		sort.Strings(names)
		add([]byte(fmt.Sprint(len(names))))
		for _, name := range names {
			b := group.files[name]
			add([]byte(name))
			add(b)
			*group.inputs = append(*group.inputs, PythonDependencyInput{Path: name, SHA256: pythonDependencyHash(b)})
		}
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

var (
	pythonInputPath         = regexp.MustCompile(`^[A-Za-z0-9._/\[\]-]+$`)
	pythonName              = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonNormalizedName    = regexp.MustCompile(`[-_.]+`)
	pythonWheelFilename     = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.]*)-([A-Za-z0-9_.+!]+)(?:-[0-9][A-Za-z0-9_]*)?-[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*-[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*-[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*\.whl$`)
	pythonDigest            = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonEncoding          = regexp.MustCompile(`coding[:=][ \t]*[-A-Za-z0-9_.]+`)
	pythonVersion           = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*(?:[-_.]?(?:a|b|c|rc|alpha|beta|pre|preview)[-_.]?[0-9]*)?(?:-[0-9]+|[-_.]?(?:post|rev|r)[-_.]?[0-9]*)?(?:[-_.]?dev[-_.]?[0-9]*)?(?:\+[a-z0-9]+(?:[-_.][a-z0-9]+)*)?$`)
	pythonWildcardVersion   = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*\.\*$`)
	pythonCompatibleVersion = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+\.[0-9]+`)
)

// Inspect each component before cleaning it away: link/../file and an escape
// followed by re-entry are unsafe even when the final lexical path is inside.
func (f *pythonDependencyFiles) resolve(dir, reference string) (string, error) {
	if !pythonInputPath.MatchString(reference) || path.IsAbs(reference) {
		return "", fmt.Errorf("unsupported local dependency reference %q", reference)
	}
	parts := strings.Split(reference, "/")
	for i, part := range parts {
		switch part {
		case "", ".":
		case "..":
			if dir == "." {
				return "", fmt.Errorf("dependency reference %q escapes the project", reference)
			}
			dir = path.Dir(dir)
		default:
			dir = path.Join(dir, part)
		}
		info, err := os.Lstat(filepath.Join(f.root, filepath.FromSlash(dir)))
		if err != nil {
			return "", fmt.Errorf("dependency reference %q: %w", reference, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("dependency reference %q traverses symlink %s", reference, dir)
		}
		if i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("dependency reference %q is not a regular file through directories", reference)
		}
	}
	return dir, nil
}

func (f *pythonDependencyFiles) visit(dir, reference string, constraint bool) error {
	name, err := f.resolve(dir, reference)
	if err != nil {
		return err
	}
	if f.active[name] {
		return fmt.Errorf("dependency reference cycle at %s", name)
	}
	role := "requirements.txt"
	if constraint {
		role = "constraints.txt"
	}
	key := role + ":" + name
	if f.visited[key] {
		return nil
	}
	f.active[name] = true
	defer delete(f.active, name)
	b, ok := f.original[name]
	if !ok {
		b, err = os.ReadFile(filepath.Join(f.root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		f.original[name] = b
	}
	lines, err := pythonDependencyLines(b)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	for i, line := range lines {
		if line == "" {
			continue
		}
		reference, nestedConstraint, include := pythonDependencyInclude(line)
		if include {
			err = f.visit(path.Dir(name), reference, nestedConstraint)
		} else {
			var requirement string
			requirement, err = pythonRequirement(line)
			if err == nil {
				f.canonical[role] = append(f.canonical[role], []byte(requirement+"\n")...)
			}
		}
		if err != nil {
			return fmt.Errorf("%s logical line %d: %w", name, i+1, err)
		}
	}
	f.visited[key] = true
	return nil
}

func pythonDependencyInclude(line string) (string, bool, bool) {
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		rest := line[len(option):]
		if len(option) > 2 {
			if strings.HasPrefix(rest, "=") {
				rest = rest[1:]
			} else if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
				continue
			}
		}
		return strings.TrimSpace(rest), option == "-c" || option == "--constraint", true
	}
	return "", false, false
}

func pythonDependencyLines(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("dependency files must be UTF-8")
	}
	text := string(b)
	for i, r := range text {
		if r == '\r' && (i+1 == len(text) || text[i+1] != '\n') {
			return nil, fmt.Errorf("bare CR in dependency file")
		}
		if strings.ContainsRune("\x00\v\f\u001c\u001d\u001e\u0085\u2028\u2029", r) || r == '\ufeff' && i != 0 {
			return nil, fmt.Errorf("unsupported dependency character U+%04X", r)
		}
	}
	text = strings.ReplaceAll(strings.TrimPrefix(text, "\ufeff"), "\r\n", "\n")
	physical := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	var lines []string
	var joined strings.Builder
	for i, line := range physical {
		backslashes := strings.Count(line, "\\")
		if backslashes > 1 || backslashes == 1 && !strings.HasSuffix(line, "\\") {
			return nil, fmt.Errorf("only a single trailing continuation backslash is supported")
		}
		// Pip does not continue a full comment line. Preserve that rule even
		// when the comment occurs in the middle of a continued logical line.
		comment := strings.HasPrefix(strings.TrimLeft(line, " \t"), "#")
		if backslashes == 1 && !comment {
			if i == len(physical)-1 {
				return nil, fmt.Errorf("dangling dependency continuation")
			}
			joined.WriteString(line[:len(line)-1])
			continue
		}
		if comment && joined.Len() > 0 {
			joined.WriteByte(' ')
		}
		joined.WriteString(line)
		logical, err := pythonDependencyComment(joined.String())
		if err != nil {
			return nil, err
		}
		lines = append(lines, strings.TrimSpace(logical))
		joined.Reset()
	}
	return lines, nil
}

func pythonDependencyComment(line string) (string, error) {
	var quote rune
	for i, r := range line {
		if r == '#' {
			if quote != 0 {
				return "", fmt.Errorf("# in a marker literal is unsupported")
			}
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				if pythonEncoding.MatchString(line[i:]) {
					return "", fmt.Errorf("encoding declarations in comments are unsupported")
				}
				return line[:i], nil
			}
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

// Split options on ASCII spaces, as pip does. In particular, Fields would
// repair a tab-delimited --hash that pip's requirement constructor rejects.
func pythonRequirement(line string) (string, error) {
	requirement, options := line, ""
	for i, r := range line {
		if r != '-' || i == 0 {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(line[:i])
		if !unicode.IsSpace(before) && before != '\x1f' {
			continue
		}
		j := i
		for j > 0 {
			prev, size := utf8.DecodeLastRuneInString(line[:j])
			if !unicode.IsSpace(prev) && prev != '\x1f' {
				break
			}
			if prev != ' ' {
				return "", fmt.Errorf("only ASCII spaces may separate a requirement from --hash")
			}
			j -= size
		}
		requirement, options = line[:j], line[i:]
		break
	}
	p := pythonRequirementParser{text: requirement}
	canonical, err := p.requirement()
	if err != nil {
		return "", err
	}
	for _, option := range strings.Split(options, " ") {
		if option == "" {
			continue
		}
		if !strings.HasPrefix(option, "--hash=") || !pythonDigest.MatchString(strings.TrimPrefix(option, "--hash=")) {
			return "", fmt.Errorf("unsupported requirement option %q; only --hash=sha256:<64 lowercase hex> is supported", option)
		}
		canonical += " " + option
	}
	return canonical, nil
}

type pythonRequirementParser struct {
	text string
	pos  int
}

func (p *pythonRequirementParser) space() {
	for p.pos < len(p.text) && (p.text[p.pos] == ' ' || p.text[p.pos] == '\t') {
		p.pos++
	}
}

func (p *pythonRequirementParser) take(token string) bool {
	p.space()
	if strings.HasPrefix(p.text[p.pos:], token) {
		p.pos += len(token)
		return true
	}
	return false
}

func (p *pythonRequirementParser) name() string {
	p.space()
	start := p.pos
	for p.pos < len(p.text) {
		c := p.text[p.pos]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", rune(c))) {
			break
		}
		p.pos++
	}
	return p.text[start:p.pos]
}

func (p *pythonRequirementParser) requirement() (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("unsupported wheel requirement %q", p.text) }
	name := p.name()
	if !pythonName.MatchString(name) {
		return invalid()
	}
	if p.take("[") {
		var extras []string
		for {
			extra := p.name()
			if !pythonName.MatchString(extra) {
				return invalid()
			}
			extras = append(extras, extra)
			if p.take("]") {
				break
			}
			if !p.take(",") {
				return invalid()
			}
		}
		name += "[" + strings.Join(extras, ",") + "]"
	}
	parentheses := p.take("(")
	var versions []string
	for {
		operator := ""
		for _, op := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
			if p.take(op) {
				operator = op
				break
			}
		}
		if operator == "" {
			if len(versions) > 0 || parentheses {
				return invalid()
			}
			break
		}
		p.space()
		start := p.pos
		for p.pos < len(p.text) && !strings.ContainsRune(" \t,;)", rune(p.text[p.pos])) {
			p.pos++
		}
		version := p.text[start:p.pos]
		wildcard := (operator == "==" || operator == "!=") && pythonWildcardVersion.MatchString(version)
		if !pythonVersion.MatchString(version) && !wildcard {
			return invalid()
		}
		if operator != "==" && operator != "!=" && operator != "===" && strings.Contains(version, "+") || operator == "~=" && !pythonCompatibleVersion.MatchString(version) {
			return invalid()
		}
		versions = append(versions, operator+version)
		if !p.take(",") {
			break
		}
	}
	if parentheses && !p.take(")") {
		return invalid()
	}
	name += strings.Join(versions, ",")
	if p.take(";") {
		p.space()
		start := p.pos
		if !p.markerOr() {
			return invalid()
		}
		name += "; " + strings.TrimSpace(p.text[start:p.pos])
	}
	p.space()
	if p.pos != len(p.text) {
		return invalid()
	}
	return name, nil
}

func (p *pythonRequirementParser) keyword(word string) bool {
	p.space()
	if !strings.HasPrefix(p.text[p.pos:], word) {
		return false
	}
	end := p.pos + len(word)
	if end < len(p.text) && (p.text[end] >= 'a' && p.text[end] <= 'z' || p.text[end] == '_') {
		return false
	}
	p.pos = end
	return true
}

func (p *pythonRequirementParser) markerOr() bool {
	if !p.markerAnd() {
		return false
	}
	for p.keyword("or") {
		if !p.markerAnd() {
			return false
		}
	}
	return true
}

func (p *pythonRequirementParser) markerAnd() bool {
	if !p.markerAtom() {
		return false
	}
	for p.keyword("and") {
		if !p.markerAtom() {
			return false
		}
	}
	return true
}

func (p *pythonRequirementParser) markerAtom() bool {
	if p.take("(") {
		return p.markerOr() && p.take(")")
	}
	if !p.markerValue() {
		return false
	}
	operator := false
	for _, op := range []string{"===", "~=", "==", "!=", "<=", ">=", "<", ">"} {
		if p.take(op) {
			operator = true
			break
		}
	}
	if !operator {
		if p.keyword("not") {
			operator = p.keyword("in")
		} else {
			operator = p.keyword("in")
		}
	}
	return operator && p.markerValue()
}

func (p *pythonRequirementParser) markerValue() bool {
	p.space()
	if p.pos == len(p.text) {
		return false
	}
	quote := p.text[p.pos]
	if quote == '\'' || quote == '"' {
		p.pos++
		start := p.pos
		for p.pos < len(p.text) && p.text[p.pos] != quote {
			c := p.text[p.pos]
			if c < ' ' || c > '~' || strings.ContainsRune("#$`\\", rune(c)) {
				return false
			}
			p.pos++
		}
		if p.pos == len(p.text) {
			return false
		}
		for _, token := range strings.Fields(p.text[start:p.pos]) {
			if strings.HasPrefix(token, "-") {
				return false
			}
		}
		p.pos++
		return true
	}
	switch p.name() {
	case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra":
		return true
	}
	return false
}

func pythonDependencyArtifacts(b []byte) ([]PythonArtifact, error) {
	var report struct {
		Version string `json:"version"`
		Install *[]struct {
			Metadata     struct{ Name, Version string } `json:"metadata"`
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
		return nil, fmt.Errorf("missing pip installation evidence")
	}
	artifacts := make([]PythonArtifact, 0, len(*report.Install))
	seen := map[string]bool{}
	for _, installed := range *report.Install {
		location, err := url.Parse(installed.DownloadInfo.URL)
		if err != nil || !path.IsAbs(location.Path) || location.User != nil ||
			(location.Scheme != "https" && location.Scheme != "file") || (location.Scheme == "https" && location.Host == "") {
			return nil, fmt.Errorf("invalid wheel URL for %q", installed.Metadata.Name)
		}
		filename := path.Base(location.Path)
		hash := "sha256:" + installed.DownloadInfo.ArchiveInfo.Hashes["sha256"]
		wheel := pythonWheelFilename.FindStringSubmatch(filename)
		if !pythonName.MatchString(installed.Metadata.Name) || !pythonVersion.MatchString(installed.Metadata.Version) || wheel == nil || !pythonVersion.MatchString(wheel[2]) || !pythonDigest.MatchString(hash) {
			return nil, fmt.Errorf("incomplete resolved wheel evidence for %q", installed.Metadata.Name)
		}
		name := strings.ToLower(pythonNormalizedName.ReplaceAllString(installed.Metadata.Name, "-"))
		if strings.ToLower(pythonNormalizedName.ReplaceAllString(wheel[1], "-")) != name {
			return nil, fmt.Errorf("wheel filename %q does not identify %s", filename, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate resolved wheel evidence for %s", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonArtifact{Name: name, Version: installed.Metadata.Version, Filename: filename, SHA256: hash})
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
