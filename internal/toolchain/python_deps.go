package toolchain

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PythonDependencies identifies the application environment, independently
// of Nagini. Image is local execution state; the remaining fields are evidence.
type PythonDependencies struct {
	Image     string                  `json:"-"`
	Base      string                  `json:"base"`
	Recipe    string                  `json:"recipe"`
	Platform  string                  `json:"platform"`
	Inputs    []PythonDependencyInput `json:"inputs"`
	Artifacts []PythonArtifact        `json:"artifacts"`
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

const pythonDepsPlatform = "linux/amd64"
const pythonDepsReport = "/opt/invariant/python-report.json"

// Keep the inputs out of Python's module search path, including when an
// input is named pip.py or venv.py. COPY a directory, never a filename glob.
const pythonDepsDockerfile = `FROM ` + PythonImage + `
COPY ["inputs/", "/opt/invariant/inputs/"]
WORKDIR /opt/invariant/build
RUN ["/usr/local/bin/python", "-I", "-m", "venv", "/opt/invariant/venv"]
ENV PIP_CONFIG_FILE=/dev/null
RUN ["/opt/invariant/venv/bin/python", "-I", "-m", "pip", "--isolated", "--disable-pip-version-check", "install", "--no-cache-dir", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--ignore-installed", "--report", "` + pythonDepsReport + `", "-r", "/opt/invariant/inputs/requirements.txt"]
ENV PATH="/opt/invariant/venv/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin"
`

var (
	pythonDepsDigest    = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonDepsEncoding  = regexp.MustCompile(`(?i)coding[ \t]*[:=][ \t]*[-a-z0-9_.]+`)
	pythonDepsName      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?`)
	pythonDepsVersion   = regexp.MustCompile(`^(?:===|==|!=|~=|<=|>=|<|>)[ \t]*[A-Za-z0-9.*+!_-]+$`)
	pythonDepsHash      = regexp.MustCompile(`^--hash(?:=|[ \t]+)(sha256:[a-fA-F0-9]{64}|sha384:[a-fA-F0-9]{96}|sha512:[a-fA-F0-9]{128})(?:[ \t]+|$)`)
	pythonDepsNormalize = regexp.MustCompile(`[-_.]+`)
)

type pythonDepsInputs struct {
	root  string
	files map[string][]byte
	dirs  map[string]bool
	stack map[string]bool
}

// PythonDepsRecipe validates the entire local input closure without Docker.
// No requirements.txt means the existing standard-library environment.
func PythonDepsRecipe(root string) (string, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.recipe(), nil
}

// PythonDepsImage builds once per recipe and reads the installed wheel report
// on every selection. A tag is only a cache key: all reads and checks use the
// immutable ID selected here, even if another build replaces that tag.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return nil, err
	}
	recipe := inputs.recipe()
	tag := "invariant-python-deps:" + strings.TrimPrefix(recipe, "sha256:")
	out, inspectErr := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	id := strings.TrimSpace(string(out))
	if inspectErr != nil {
		dir, err := os.MkdirTemp("", "invariant-python-deps-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		contextDir := filepath.Join(dir, "context")
		if err := inputs.stage(contextDir); err != nil {
			return nil, fmt.Errorf("staging Python dependency inputs: %w", err)
		}
		iidfile := filepath.Join(dir, "image-id")
		cmd := exec.CommandContext(ctx, "docker", "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, contextDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building Python application dependencies: %w\n%s", err, out)
		}
		out, err = os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("reading Python dependency image ID: %w", err)
		}
		id = strings.TrimSpace(string(out))
	}
	if !pythonDepsDigest.MatchString(id) {
		return nil, fmt.Errorf("Python dependency image has no immutable Docker ID: %q", id)
	}
	// Application wheels can install console scripts named cat or sh. The
	// reader must come from the pinned base, outside the application venv.
	out, err = Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", id, "/bin/cat", pythonDepsReport).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation report: %w\n%s", err, out)
	}
	artifacts, err := pythonDepsArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("invalid Python dependency installation report: %w", err)
	}
	env := &PythonDependencies{Image: id, Base: PythonImage, Recipe: recipe, Platform: pythonDepsPlatform, Artifacts: artifacts}
	for _, name := range inputs.paths() {
		env.Inputs = append(env.Inputs, PythonDependencyInput{Path: name, SHA256: pythonDepsSHA256(inputs.files[name])})
	}
	return env, nil
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
	inputs := &pythonDepsInputs{root: root, files: map[string][]byte{}, dirs: map[string]bool{}, stack: map[string]bool{}}
	name, err := inputs.reference(".", "requirements.txt")
	if err == nil {
		err = inputs.read(name)
	}
	if err != nil {
		return nil, fmt.Errorf("Python dependencies: %w", err)
	}
	return inputs, nil
}

// Walk the spelling, not just the cleaned target: a/../b still traverses a.
// This both checks confinement and preserves empty directories pip must open.
func (in *pythonDepsInputs) reference(base, ref string) (string, error) {
	if ref == "" || filepath.IsAbs(ref) || strings.ContainsAny(ref, "\\:$\x00") {
		return "", fmt.Errorf("unsupported requirements reference %q", ref)
	}
	current := base
	parts := strings.Split(ref, "/")
	for i, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if current == "." {
				return "", fmt.Errorf("requirements reference %q leaves the project", ref)
			}
			current = path.Dir(current)
		default:
			current = path.Join(current, part)
			info, err := os.Lstat(filepath.Join(in.root, filepath.FromSlash(current)))
			if err != nil {
				return "", fmt.Errorf("requirements reference %q: %w", ref, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("requirements reference %q traverses symlink %s", ref, current)
			}
			if i < len(parts)-1 {
				if !info.IsDir() {
					return "", fmt.Errorf("requirements reference %q traverses non-directory %s", ref, current)
				}
				in.dirs[current] = true
			} else if !info.Mode().IsRegular() {
				return "", fmt.Errorf("requirements reference %q is not a regular file", ref)
			}
		}
	}
	info, err := os.Lstat(filepath.Join(in.root, filepath.FromSlash(current)))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("requirements reference %q must name a regular file", ref)
	}
	return current, nil
}

func (in *pythonDepsInputs) read(name string) error {
	if in.stack[name] {
		return fmt.Errorf("requirements reference cycle at %s", name)
	}
	if _, ok := in.files[name]; ok {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(in.root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	if err := pythonDepsEncodingCheck(b); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	in.files[name] = b
	in.stack[name] = true
	defer delete(in.stack, name)
	text := strings.TrimPrefix(string(b), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var logical string
	for i, line := range strings.Split(text, "\n") {
		comment := strings.HasPrefix(strings.TrimSpace(line), "#")
		if strings.HasSuffix(line, "\\") && !comment {
			logical += strings.TrimSuffix(line, "\\")
			continue
		}
		if comment {
			logical += " "
		}
		logical += line
		if err := in.line(name, logical); err != nil {
			return fmt.Errorf("%s:%d: %w", name, i+1, err)
		}
		logical = ""
	}
	if logical != "" {
		return fmt.Errorf("%s: unfinished continuation", name)
	}
	return nil
}

func pythonDepsEncodingCheck(b []byte) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("requirements must be UTF-8, with an optional leading UTF-8 BOM")
	}
	for i, c := range b {
		if c == '\r' && (i+1 == len(b) || b[i+1] != '\n') {
			return fmt.Errorf("bare CR is not allowed; use LF or CRLF line endings")
		}
	}
	if strings.ContainsAny(string(b), "\v\f\x1c\x1d\x1e\u0085\u2028\u2029\x00") {
		return fmt.Errorf("unsupported requirements line separator or NUL")
	}
	if pythonDepsEncoding.Match(b) {
		return fmt.Errorf("encoding declarations are not allowed in requirements files")
	}
	return nil
}

func (in *pythonDepsInputs) line(name, line string) error {
	// pip removes whitespace-prefixed comments after joining continuations.
	previous := rune(' ')
	for i, r := range line {
		if r == '#' && unicode.IsSpace(previous) {
			line = line[:i]
			break
		}
		previous = r
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if strings.Contains(line, "${") {
		return fmt.Errorf("environment substitutions are not allowed")
	}
	if strings.HasPrefix(line, "-") {
		value := ""
		for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
			if !strings.HasPrefix(line, option) {
				continue
			}
			value = strings.TrimPrefix(line, option)
			if strings.HasPrefix(option, "--") {
				if len(value) == 0 || (value[0] != '=' && value[0] != ' ' && value[0] != '\t') {
					return fmt.Errorf("unsupported pip option in %q", line)
				}
				value = strings.TrimPrefix(value, "=")
			}
			break
		}
		ref, err := pythonDepsReferenceValue(strings.TrimSpace(value))
		if err != nil {
			return err
		}
		ref, err = in.reference(path.Dir(name), ref)
		if err != nil {
			return err
		}
		return in.read(ref)
	}
	// Options can follow a requirement, but only artifact hashes are allowed.
	// pip splits at whitespace followed by '-', before parsing its markers.
	for i := 1; i < len(line); i++ {
		if line[i] != '-' || (line[i-1] != ' ' && line[i-1] != '\t') {
			continue
		}
		options := line[i:]
		for options != "" {
			match := pythonDepsHash.FindString(options)
			if match == "" {
				return fmt.Errorf("only --hash is allowed after a wheel requirement")
			}
			options = strings.TrimSpace(strings.TrimPrefix(options, match))
		}
		line = strings.TrimSpace(line[:i])
		break
	}
	// The source portion is a named PyPI requirement. pip validates full
	// version and marker semantics; none of that text is rewritten for pip.
	source, _, _ := strings.Cut(line, ";")
	source = strings.TrimSpace(source)
	packageName := pythonDepsName.FindString(source)
	if packageName == "" {
		return fmt.Errorf("expected a named PyPI wheel requirement: %q", line)
	}
	rest := strings.TrimSpace(source[len(packageName):])
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return fmt.Errorf("invalid requirement extras: %q", line)
		}
		for _, extra := range strings.Split(rest[1:end], ",") {
			extra = strings.TrimSpace(extra)
			if extra == "" || pythonDepsName.FindString(extra) != extra {
				return fmt.Errorf("invalid requirement extra: %q", extra)
			}
		}
		rest = strings.TrimSpace(rest[end+1:])
	}
	if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
		rest = strings.TrimSpace(rest[1 : len(rest)-1])
	}
	if rest != "" {
		for _, version := range strings.Split(rest, ",") {
			if !pythonDepsVersion.MatchString(strings.TrimSpace(version)) {
				return fmt.Errorf("unsupported wheel requirement source or version: %q", line)
			}
		}
	}
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz", ".tar.xz", ".txz"} {
		if strings.HasSuffix(strings.ToLower(source), suffix) {
			return fmt.Errorf("local archives are not supported: %q", line)
		}
	}
	return nil
}

func pythonDepsReferenceValue(value string) (string, error) {
	// A single literal argument, optionally quoted. Shell escapes and extra
	// options would give pip a different reference and are rejected here.
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
		quote := value[0]
		value = value[1 : len(value)-1]
		if strings.ContainsRune(value, rune(quote)) {
			return "", fmt.Errorf("expected one local requirements reference")
		}
	} else if strings.ContainsAny(value, " \t'\"") {
		return "", fmt.Errorf("expected one local requirements reference")
	}
	if value == "" || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("only local -r/--requirement and -c/--constraint references are supported")
	}
	return value, nil
}

func (in *pythonDepsInputs) paths() []string {
	var names []string
	for name := range in.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (in *pythonDepsInputs) directories() []string {
	var names []string
	for name := range in.dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (in *pythonDepsInputs) recipe() string {
	h := sha256.New()
	pythonDepsField(h, []byte(pythonDepsDockerfile))
	pythonDepsField(h, []byte(pythonDepsPlatform))
	for _, dir := range in.directories() {
		pythonDepsField(h, []byte("directory"))
		pythonDepsField(h, []byte(dir))
	}
	for _, name := range in.paths() {
		pythonDepsField(h, []byte("file"))
		pythonDepsField(h, []byte(name))
		pythonDepsField(h, in.files[name])
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func pythonDepsField(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	h.Write(size[:])
	h.Write(b)
}

func pythonDepsSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (in *pythonDepsInputs) stage(dir string) error {
	inputs := filepath.Join(dir, "inputs")
	if err := os.MkdirAll(inputs, 0o755); err != nil {
		return err
	}
	for _, name := range in.directories() {
		if err := os.MkdirAll(filepath.Join(inputs, filepath.FromSlash(name)), 0o755); err != nil {
			return err
		}
	}
	for _, name := range in.paths() {
		filename := filepath.Join(inputs, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filename, in.files[name], 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644)
}

func pythonDepsArtifacts(b []byte) ([]PythonArtifact, error) {
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
			Metadata struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"metadata"`
		} `json:"install"`
	}
	if err := json.Unmarshal(b, &report); err != nil {
		return nil, err
	}
	if report.Version != "1" || report.PipVersion == "" || report.Install == nil {
		return nil, fmt.Errorf("expected a pip version 1 report with an install list")
	}
	artifacts := make([]PythonArtifact, 0, len(report.Install))
	names := map[string]bool{}
	for _, item := range report.Install {
		name := item.Metadata.Name
		if name == "" || pythonDepsName.FindString(name) != name || strings.TrimSpace(item.Metadata.Version) == "" {
			return nil, fmt.Errorf("wheel is missing its package name or version")
		}
		name = pythonDepsNormalize.ReplaceAllString(strings.ToLower(name), "-")
		u, err := url.Parse(item.DownloadInfo.URL)
		if err != nil || u.Scheme == "" || !strings.HasSuffix(u.Path, ".whl") {
			return nil, fmt.Errorf("%s has no resolved wheel URL", name)
		}
		filename := path.Base(u.Path)
		digest := "sha256:" + strings.ToLower(item.DownloadInfo.ArchiveInfo.Hashes["sha256"])
		if !pythonDepsDigest.MatchString(digest) {
			return nil, fmt.Errorf("%s wheel has no valid SHA-256 artifact hash", name)
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate resolved distribution %s", name)
		}
		names[name] = true
		artifacts = append(artifacts, PythonArtifact{Name: name, Version: item.Metadata.Version, Filename: filename, SHA256: digest})
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
