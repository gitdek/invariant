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

const pythonPlatform = "linux/amd64"

// PythonLibrary is deliberately neither a site directory nor on PATH. Wheels
// are application libraries, not extensions to the gate's interpreter.
const PythonLibrary = "/opt/invariant-libraries"
const pythonReportPath = "/opt/invariant-evidence/install.json"

const pythonDepsDockerfile = "FROM " + PythonImage + `
WORKDIR /opt/invariant-evidence
COPY canonical/ /opt/invariant-inputs/
RUN /usr/local/bin/python -I -m pip --isolated install --disable-pip-version-check --no-cache-dir --index-url https://pypi.org/simple --only-binary=:all: --ignore-installed --target ` + PythonLibrary + ` --report ` + pythonReportPath + ` -r /opt/invariant-inputs/requirements.txt -c /opt/invariant-inputs/constraints.txt
`

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

// PythonDependencies records the actual resolution, including on cache hits.
// Image selects execution but is not portable receipt evidence.
type PythonDependencies struct {
	Image           string           `json:"-"`
	Base            string           `json:"base"`
	Recipe          string           `json:"recipe"`
	Platform        string           `json:"platform"`
	Inputs          []PythonInput    `json:"inputs"`
	CanonicalInputs []PythonInput    `json:"canonical_inputs"`
	Artifacts       []PythonArtifact `json:"artifacts"`
}

type pythonFile struct {
	Path       string `json:"path"`
	Constraint bool   `json:"constraint"`
}
type pythonInclude struct {
	Source     string `json:"source"`
	Target     string `json:"target"`
	Value      string `json:"value"`
	Constraint bool   `json:"constraint"`
}
type pythonOption struct {
	Source string `json:"source"`
	Line   int    `json:"line"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}
type pythonParseError struct {
	Source  string `json:"source"`
	Line    int    `json:"line"`
	Type    string `json:"type"`
	Message string `json:"message"`
}
type pythonRequirement struct {
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
	Options    []pythonOption      `json:"options"`
}
type pythonParseReport struct {
	Schema       int                 `json:"schema"`
	PipVersion   string              `json:"pip_version"`
	Root         string              `json:"root"`
	Files        []pythonFile        `json:"files"`
	Includes     []pythonInclude     `json:"includes"`
	Requirements []pythonRequirement `json:"requirements"`
	Options      []pythonOption      `json:"options"`
	Errors       []pythonParseError  `json:"errors"`
}

type pythonInputs struct {
	root      string
	bytes     map[string][]byte
	files     map[pythonFile]bool
	includes  map[pythonInclude]bool
	canonical map[string][]byte
}

var (
	pythonName      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pythonHead      = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(\[[^\]]*\])?`)
	pythonVersion   = regexp.MustCompile(`^(===|==|!=|~=|<=|>=|<|>)\s*([A-Za-z0-9][A-Za-z0-9.*+!_-]*)$`)
	pythonHash      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonEncoding  = regexp.MustCompile(`coding[:=]\s*[-\w.]+`)
	pythonNormalize = regexp.MustCompile(`[-_.]+`)
)

func pythonDigest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

// PythonDepsRecipe performs the same preflight and authoritative offline parse
// as image selection, but never builds an application image.
func PythonDepsRecipe(root string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	inputs, err := preparePythonInputs(ctx, root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.environment().Recipe, nil
}

func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := preparePythonInputs(ctx, root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.environment()
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
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
		for name, body := range inputs.canonical {
			if err := os.WriteFile(filepath.Join(dir, "canonical", name), body, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		// BuildKit writes the ID of this build, independent of a concurrent
		// builder replacing the tag before the evidence is read.
		iid := filepath.Join(dir, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonPlatform, "--iidfile", iid, "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies (public-PyPI wheels only): %w\n%s", err, out)
		}
		id, err = os.ReadFile(iid)
		if err != nil {
			return nil, fmt.Errorf("reading Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(id))
	if !pythonHash.MatchString(env.Image) {
		return nil, fmt.Errorf("invalid immutable Python dependency image ID %q", env.Image)
	}
	report, err := ReadPythonDepsReport(ctx, env.Image)
	if err != nil {
		return nil, err
	}
	env.Artifacts, err = pythonArtifacts(report)
	if err != nil {
		return nil, fmt.Errorf("Python dependency installation evidence: %w", err)
	}
	return env, nil
}

// ReadPythonDepsReport is also used by direct-image integration fixtures. It
// reads evidence only; admission validates that evidence separately.
func ReadPythonDepsReport(ctx context.Context, image string) ([]byte, error) {
	if !pythonHash.MatchString(image) {
		return nil, fmt.Errorf("Python evidence requires an immutable image ID")
	}
	cmd := Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", pythonPlatform, "--read-only", image, "/bin/cat", pythonReportPath)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation report: %w: %s", err, diagnostic.String())
	}
	return b, nil
}

func pythonArtifacts(b []byte) ([]PythonArtifact, error) {
	var report struct {
		Version string `json:"version"`
		Install *[]struct {
			Download struct {
				URL     string `json:"url"`
				Archive struct {
					Hashes map[string]string `json:"hashes"`
				} `json:"archive_info"`
			} `json:"download_info"`
			Direct   bool                           `json:"is_direct"`
			Metadata struct{ Name, Version string } `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, err
	}
	if report.Version != "1" || report.Install == nil {
		return nil, fmt.Errorf("missing or unsupported pip installation report")
	}
	artifacts := []PythonArtifact{}
	for _, row := range *report.Install {
		u, err := url.Parse(row.Download.URL)
		if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || row.Direct {
			return nil, fmt.Errorf("artifact is not a public-PyPI wheel: %q", row.Download.URL)
		}
		filename := path.Base(u.Path)
		hash := "sha256:" + row.Download.Archive.Hashes["sha256"]
		if !strings.HasSuffix(filename, ".whl") || !pythonName.MatchString(row.Metadata.Name) || row.Metadata.Version == "" || !pythonHash.MatchString(hash) {
			return nil, fmt.Errorf("incomplete wheel name, version, filename or SHA-256: %q", row.Metadata.Name)
		}
		artifacts = append(artifacts, PythonArtifact{pythonNormalize.ReplaceAllString(strings.ToLower(row.Metadata.Name), "-"), row.Metadata.Version, filename, hash})
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

func (p *pythonInputs) environment() *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonPlatform}
	h := sha256.New()
	add := func(b []byte) { fmt.Fprintf(h, "%d\n", len(b)); h.Write(b) }
	for _, s := range []string{pythonDepsDockerfile, PythonImage, pythonPlatform} {
		add([]byte(s))
	}
	for _, collection := range []struct {
		values map[string][]byte
		output *[]PythonInput
	}{{p.bytes, &env.Inputs}, {p.canonical, &env.CanonicalInputs}} {
		keys := make([]string, 0, len(collection.values))
		for k := range collection.values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b := collection.values[k]
			add([]byte(k))
			add(b)
			*collection.output = append(*collection.output, PythonInput{k, pythonDigest(b)})
		}
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

func preparePythonInputs(ctx context.Context, root string) (*pythonInputs, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	p := &pythonInputs{root: root, bytes: map[string][]byte{}, files: map[pythonFile]bool{}, includes: map[pythonInclude]bool{}}
	if err := p.preflight("requirements.txt", false, map[string]bool{}); err != nil {
		return nil, err
	}
	if err := ensurePythonBase(ctx); err != nil {
		return nil, err
	}
	cmd := Docker(ctx, "run", "--rm", "--pull=never", "--network", "none", "--platform", pythonPlatform, "--read-only", "-v", root+":/project:ro", "-w", "/tmp", PythonImage, "/usr/local/bin/python", "-I", "-B", "-c", pythonPipParser)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("parsing Python dependencies with pinned pip: %w: %s", err, diagnostic.String())
	}
	var report pythonParseReport
	if err := pythonCompleteJSON(b, reflect.TypeOf(report), "report"); err != nil {
		return nil, fmt.Errorf("pinned pip report: %w", err)
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, fmt.Errorf("invalid pinned pip report: %w", err)
	}
	if err := p.acceptReport(report); err != nil {
		return nil, err
	}
	// A concurrent application edit must not bind new parser semantics to old
	// provenance. The installation context contains only the accepted copies.
	for name, before := range p.bytes {
		if _, err := p.resolve("", name); err != nil {
			return nil, err
		}
		after, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(before, after) {
			return nil, fmt.Errorf("Python input %s changed during parsing; retry", name)
		}
	}
	return p, nil
}

func ensurePythonBase(ctx context.Context) error {
	out, err := Docker(ctx, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", PythonImage).Output()
	if err == nil && strings.TrimSpace(string(out)) == pythonPlatform {
		return nil
	}
	out, err = Docker(ctx, "pull", "--platform", pythonPlatform, PythonImage).CombinedOutput()
	if err != nil {
		return fmt.Errorf("acquiring pinned Python base %s for %s; check Docker and registry access, then retry: %w\n%s", PythonImage, pythonPlatform, err, out)
	}
	return nil
}

// Presence is checked recursively before interpreting any semantic policy.
// Go's zero values alone cannot distinguish an omitted pin from an empty pin.
func pythonCompleteJSON(b []byte, typ reflect.Type, field string) error {
	if len(b) == 0 || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return fmt.Errorf("required field %s is missing or null", field)
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(b, &fields); err != nil {
			return err
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name := f.Tag.Get("json")
			if err := pythonCompleteJSON(fields[name], f.Type, field+"."+name); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var rows []json.RawMessage
		if err := json.Unmarshal(b, &rows); err != nil {
			return err
		}
		for i, row := range rows {
			if err := pythonCompleteJSON(row, typ.Elem(), fmt.Sprintf("%s[%d]", field, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		var rows map[string]json.RawMessage
		if err := json.Unmarshal(b, &rows); err != nil {
			return err
		}
		for key, row := range rows {
			if err := pythonCompleteJSON(row, typ.Elem(), field+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *pythonInputs) acceptReport(r pythonParseReport) error {
	if r.Schema != 1 || r.PipVersion == "" || r.Root != "requirements.txt" {
		return fmt.Errorf("invalid pinned pip report identity")
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("pinned pip refused %s:%d: %s", r.Errors[0].Source, r.Errors[0].Line, r.Errors[0].Message)
	}
	if len(r.Options) > 0 {
		return fmt.Errorf("pinned pip reported unsupported option %s", r.Options[0].Name)
	}
	files := map[pythonFile]bool{}
	for _, f := range r.Files {
		if !p.files[f] {
			return fmt.Errorf("pinned pip reported unexpected file or role %s", f.Path)
		}
		files[f] = true
	}
	if !reflect.DeepEqual(files, p.files) {
		return fmt.Errorf("pinned pip report is missing followed files or roles")
	}
	includes := map[pythonInclude]bool{}
	for _, i := range r.Includes {
		if !p.includes[i] {
			return fmt.Errorf("pinned pip reported unsafe or unexpected include %q from %s", i.Value, i.Source)
		}
		includes[i] = true
	}
	if !reflect.DeepEqual(includes, p.includes) {
		return fmt.Errorf("pinned pip report is missing includes")
	}
	p.canonical = map[string][]byte{"requirements.txt": {}, "constraints.txt": {}}
	for _, row := range r.Requirements {
		if !files[pythonFile{row.Source, row.Constraint}] || row.Line < 1 {
			return fmt.Errorf("pinned pip requirement has invalid source %s", row.Source)
		}
		if row.URL != "" || row.Link != "" || row.Path != "" || row.Archive || row.Editable || len(row.Options) != 0 {
			return fmt.Errorf("pinned pip reported a source or option outside named PyPI requirements in %s", row.Source)
		}
		if !pythonName.MatchString(row.Name) {
			return fmt.Errorf("pinned pip reported invalid requirement name %q", row.Name)
		}
		line := row.Name
		if len(row.Extras) > 0 {
			for _, extra := range row.Extras {
				if !pythonName.MatchString(extra) {
					return fmt.Errorf("invalid pip extra %q", extra)
				}
			}
			sort.Strings(row.Extras)
			line += "[" + strings.Join(row.Extras, ",") + "]"
		}
		line += row.Specifier
		if row.Marker != "" {
			line += "; " + row.Marker
		}
		for algorithm, hashes := range row.Hashes {
			if algorithm != "sha256" || len(hashes) == 0 {
				return fmt.Errorf("unsupported or empty pip hash %s", algorithm)
			}
			sort.Strings(hashes)
			for _, hash := range hashes {
				if !pythonHash.MatchString(algorithm + ":" + hash) {
					return fmt.Errorf("invalid pip SHA-256")
				}
				line += " --hash=" + algorithm + ":" + hash
			}
		}
		if err := pythonRequirementSyntax(line); err != nil {
			return fmt.Errorf("invalid pip requirement report: %w", err)
		}
		name := "requirements.txt"
		if row.Constraint {
			name = "constraints.txt"
		}
		p.canonical[name] = append(p.canonical[name], []byte(line+"\n")...)
	}
	return nil
}

// resolve checks every traversed component before cleaning .. away. Even a
// canceled symlink or an excursion outside the project is not a local input.
func (p *pythonInputs) resolve(source, value string) (string, error) {
	if value == "" || filepath.IsAbs(value) || strings.ContainsAny(value, "\\:$\x00\"' ") || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("unsupported Python include %q", value)
	}
	parts := []string{}
	if source != "" && path.Dir(source) != "." {
		parts = strings.Split(path.Dir(source), "/")
	}
	components := strings.Split(value, "/")
	for index, part := range components {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("Python include escapes project: %q", value)
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
		info, err := os.Lstat(filepath.Join(p.root, filepath.FromSlash(strings.Join(parts, "/"))))
		if err != nil {
			return "", fmt.Errorf("Python include %q: %w", value, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink in Python include %q", value)
		}
		if index < len(components)-1 && !info.IsDir() {
			return "", fmt.Errorf("non-directory traversed by Python include %q", value)
		}
	}
	name := strings.Join(parts, "/")
	info, err := os.Lstat(filepath.Join(p.root, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("Python input %q is not a regular file", name)
	}
	return name, nil
}

func (p *pythonInputs) preflight(name string, constraint bool, active map[string]bool) error {
	if active[name] {
		return fmt.Errorf("Python include cycle at %s", name)
	}
	if p.files[pythonFile{name, constraint}] {
		return nil
	}
	if _, err := p.resolve("", name); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(p.root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	lines, err := pythonLogicalLines(b)
	if err != nil {
		return fmt.Errorf("Python input %s: %w", name, err)
	}
	p.bytes[name], p.files[pythonFile{name, constraint}], active[name] = b, true, true
	defer delete(active, name)
	for _, line := range lines {
		value, role, include, err := pythonReference(line)
		if err != nil {
			return fmt.Errorf("Python input %s: %w", name, err)
		}
		if include {
			target, err := p.resolve(name, value)
			if err != nil {
				return err
			}
			p.includes[pythonInclude{name, target, value, role}] = true
			if err := p.preflight(target, role, active); err != nil {
				return err
			}
		} else if err := pythonRequirementSyntax(line); err != nil {
			return fmt.Errorf("Python input %s: %w", name, err)
		}
	}
	return nil
}

func pythonLogicalLines(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	if strings.ContainsAny(s, "\v\f\u001c\u001d\u001e\u0085\u2028\u2029\ufeff") {
		return nil, fmt.Errorf("unsupported requirement line separator")
	}
	for i := range s {
		if s[i] == '\r' && (i+1 == len(s) || s[i+1] != '\n') {
			return nil, fmt.Errorf("bare CR in requirements")
		}
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var result []string
	logical := ""
	continued := false
	physical := strings.Split(s, "\n")
	for i, line := range physical {
		if at := strings.IndexByte(line, '#'); at >= 0 && pythonEncoding.MatchString(line[at:]) {
			return nil, fmt.Errorf("encoding declarations are unsupported")
		}
		if strings.Contains(line, "\\") {
			if strings.Count(line, "\\") != 1 || !strings.HasSuffix(line, "\\") || i >= len(physical)-1 || (i == len(physical)-2 && physical[i+1] == "") {
				return nil, fmt.Errorf("only a single trailing continuation is supported")
			}
			logical += strings.TrimSuffix(line, "\\")
			continued = true
			continue
		}
		logical += line
		continued = false
		// Comments begin outside marker literals. Literal # is excluded even
		// if pip's own earlier comment pass would swallow the closing quote.
		var quote rune
		end := len(logical)
		for at, r := range logical {
			if r == '#' {
				if quote != 0 {
					return nil, fmt.Errorf("# in a marker literal is unsupported")
				}
				if at == 0 || unicode.IsSpace(rune(logical[at-1])) {
					end = at
					break
				}
			}
			if r == '\'' || r == '"' {
				if quote == r {
					quote = 0
				} else if quote == 0 {
					quote = r
				}
			}
		}
		if value := strings.TrimSpace(logical[:end]); value != "" {
			result = append(result, value)
		}
		logical = ""
	}
	if continued {
		return nil, fmt.Errorf("dangling continuation")
	}
	return result, nil
}

func pythonReference(line string) (string, bool, bool, error) {
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		value := strings.TrimPrefix(line, option)
		if len(option) > 2 {
			if strings.HasPrefix(value, "=") {
				value = value[1:]
				if value == "" || value[0] == ' ' || value[0] == '\t' {
					return "", false, true, fmt.Errorf("empty include value")
				}
			} else if strings.HasPrefix(value, " ") {
				value = strings.TrimLeft(value, " ")
			} else {
				return "", false, true, fmt.Errorf("invalid include option")
			}
		} else {
			value = strings.TrimLeft(value, " \t")
		}
		if value == "" || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
			return "", false, true, fmt.Errorf("invalid include value %q", value)
		}
		return value, option == "-c" || option == "--constraint", true, nil
	}
	return "", false, false, nil
}

func pythonRequirementSyntax(line string) error {
	bad := func() error { return fmt.Errorf("unsupported named PyPI requirement %q", line) }
	if strings.ContainsAny(line, "\\\r\n#$@") {
		return bad()
	}
	// Pip's option splitter uses ASCII spaces, not general whitespace.
	if at := strings.Index(line, "--"); at >= 0 {
		start := at
		for start > 0 && line[start-1] == ' ' {
			start--
		}
		if start == at {
			return bad()
		}
		if start > 0 {
			r, _ := utf8.DecodeLastRuneInString(line[:start])
			if unicode.IsSpace(r) || r == '\u001f' {
				return bad()
			}
		}
		for _, hash := range strings.Split(line[at:], " ") {
			if hash == "" {
				continue
			}
			if !strings.HasPrefix(hash, "--hash=") || !pythonHash.MatchString(strings.TrimPrefix(hash, "--hash=")) {
				return bad()
			}
		}
		line = line[:start]
	}
	head, marker, hasMarker := strings.Cut(line, ";")
	m := pythonHead.FindStringSubmatch(strings.TrimSpace(head))
	if m == nil || !pythonName.MatchString(m[1]) {
		return bad()
	}
	for _, suffix := range []string{".whl", ".zip", ".tar.gz", ".tar.bz2", ".tar.xz", ".tgz"} {
		if strings.HasSuffix(strings.ToLower(m[1]), suffix) {
			return bad()
		}
	}
	if m[2] != "" {
		for _, extra := range strings.Split(m[2][1:len(m[2])-1], ",") {
			if !pythonName.MatchString(strings.TrimSpace(extra)) {
				return bad()
			}
		}
	}
	versions := strings.TrimSpace(strings.TrimSpace(head)[len(m[0]):])
	if strings.HasPrefix(versions, "(") && strings.HasSuffix(versions, ")") {
		versions = strings.TrimSpace(versions[1 : len(versions)-1])
	}
	if versions != "" {
		for _, version := range strings.Split(versions, ",") {
			if !pythonVersion.MatchString(strings.TrimSpace(version)) {
				return bad()
			}
		}
	}
	if hasMarker && !pythonMarkerValid(strings.TrimSpace(marker)) {
		return bad()
	}
	return nil
}

// A small recursive grammar prevents malformed suffixes from reaching Docker.
// The pinned pip constructor remains authoritative for PEP 508 semantics.
func pythonMarkerValid(s string) bool {
	var tokens []string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		if s[0] == '\'' || s[0] == '"' {
			end := strings.IndexByte(s[1:], s[0])
			if end < 0 {
				return false
			}
			end += 2
			value := s[:end]
			for _, word := range strings.Fields(value[1 : len(value)-1]) {
				if strings.HasPrefix(word, "-") {
					return false
				}
			}
			tokens = append(tokens, value)
			s = s[end:]
			continue
		}
		if strings.ContainsRune("()", rune(s[0])) {
			tokens = append(tokens, s[:1])
			s = s[1:]
			continue
		}
		if strings.ContainsRune("<>=!~", rune(s[0])) {
			end := 1
			for end < len(s) && strings.ContainsRune("<>=!~", rune(s[end])) {
				end++
			}
			tokens = append(tokens, s[:end])
			s = s[end:]
			continue
		}
		end := 0
		for end < len(s) && ((s[end] >= 'a' && s[end] <= 'z') || s[end] == '_') {
			end++
		}
		if end == 0 {
			return false
		}
		tokens = append(tokens, s[:end])
		s = s[end:]
	}
	i := 0
	operand := func() bool {
		if i >= len(tokens) {
			return false
		}
		t := tokens[i]
		i++
		if strings.HasPrefix(t, "'") || strings.HasPrefix(t, "\"") {
			return true
		}
		switch t {
		case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra":
			return true
		}
		return false
	}
	var expression, atom func() bool
	atom = func() bool {
		if i < len(tokens) && tokens[i] == "(" {
			i++
			if !expression() || i >= len(tokens) || tokens[i] != ")" {
				return false
			}
			i++
			return true
		}
		if !operand() || i >= len(tokens) {
			return false
		}
		op := tokens[i]
		i++
		if op == "not" {
			if i >= len(tokens) || tokens[i] != "in" {
				return false
			}
			i++
			op = "in"
		}
		switch op {
		case "==", "===", "!=", "~=", "<=", ">=", "<", ">", "in":
		default:
			return false
		}
		return operand()
	}
	expression = func() bool {
		if !atom() {
			return false
		}
		for i < len(tokens) && (tokens[i] == "and" || tokens[i] == "or") {
			i++
			if !atom() {
				return false
			}
		}
		return true
	}
	return expression() && i == len(tokens)
}

// Observe pip before its recursive iterator consumes include/option-only
// lines. The same pip constructors used by installation supply identities,
// separate links, unevaluated markers and hashes; no resolver runs here.
const pythonPipParser = `
import json, os, shlex
import pip
from pip._internal.network.session import PipSession
from pip._internal.req import req_file
from pip._internal.req.constructors import install_req_from_parsed_requirement
from pip._vendor.packaging.utils import canonicalize_name

report = dict(schema=1, pip_version=pip.__version__, root="requirements.txt",
              files=[], includes=[], requirements=[], options=[], errors=[])
raw_options = {}
line_parser = req_file.get_line_parser
def observed_parser(*args, **kwargs):
    parse = line_parser(*args, **kwargs)
    def observe(text):
        result = parse(text)
        raw_options[id(result[1])] = shlex.split(req_file.break_args_options(text)[1])
        return result
    return observe
req_file.get_line_parser = observed_parser

def relative(filename):
    return os.path.relpath(filename, "/project")
def error(source, line, exc):
    report["errors"].append(dict(source=source, line=line, type=type(exc).__name__, message=str(exc)))
def options(line, source):
    words = raw_options.pop(id(line.opts), [])
    result = []
    i = 0
    while i < len(words):
        word = words[i]
        name, equal, value = word.partition("=")
        if name in ("-r", "-c", "--requirement", "--constraint", "--hash"):
            if not equal and i+1 < len(words): i += 1
        elif word.startswith("-r") and not word.startswith("--"):
            pass
        elif word.startswith("-c") and not word.startswith("--"):
            pass
        else:
            if not equal and i+1 < len(words) and not words[i+1].startswith("-"):
                i += 1
                value = words[i]
            result.append(dict(source=source, line=line.lineno, name=name, value=value))
        i += 1
    return result

parse_file = req_file.RequirementsFileParser._parse_file
def observe_file(self, filename, constraint):
    source = relative(filename)
    f = dict(path=source, constraint=constraint)
    if f not in report["files"]: report["files"].append(f)
    lineno = 0
    try:
        for line in parse_file(self, filename, constraint):
            lineno = line.lineno
            opts = options(line, source)
            if line.requirement is None:
                report["options"].extend(opts)
                for values, role in [(line.opts.requirements, False), (line.opts.constraints, True)]:
                    for value in values or []:
                        target = relative(os.path.normpath(os.path.join(os.path.dirname(filename), value)))
                        inc = dict(source=source, target=target, value=value, constraint=role)
                        if inc not in report["includes"]: report["includes"].append(inc)
            else:
                try:
                    parsed = req_file.handle_requirement_line(line)
                    install = install_req_from_parsed_requirement(parsed, isolated=True)
                    req = install.req
                    link = install.link
                    report["requirements"].append(dict(
                        source=source, line=lineno, name=canonicalize_name(req.name) if req else "",
                        extras=sorted({canonicalize_name(x) for x in req.extras}) if req else [],
                        specifier=str(req.specifier) if req else "",
                        marker=str(install.markers) if install.markers else "",
                        hashes={k: sorted(set(v)) for k,v in install.hash_options.items()},
                        constraint=install.constraint, url=(req.url or "") if req else "",
                        link=str(link) if link else "", path=link.file_path if link and link.is_file else "",
                        archive=bool(link and link.is_wheel), editable=install.editable, options=opts))
                except Exception as exc:
                    error(source, lineno, exc)
            yield line
    except Exception as exc:
        error(source, lineno, exc)
        raise
req_file.RequirementsFileParser._parse_file = observe_file
def no_network(*args, **kwargs):
    raise RuntimeError("offline pip parser attempted network access")
try:
    with PipSession() as session:
        session.request = no_network
        list(req_file.parse_requirements("/project/requirements.txt", session=session))
except Exception as exc:
    if not report["errors"]: error("requirements.txt", 0, exc)
print(json.dumps(report, sort_keys=True))
`
