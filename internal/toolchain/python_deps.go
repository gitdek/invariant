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
const pythonDepsReport = "/build/python-dependencies.json"

// Inputs are copied as a directory: individual COPY sources interpret glob
// characters in filenames. Python runs outside that directory, in isolated
// mode, so an input named pip.py or venv.py cannot become a build tool.
const pythonDepsDockerfile = "FROM " + PythonImage + `
COPY ["inputs/", "/inputs/"]
WORKDIR /build
RUN ["/usr/local/bin/python", "-I", "-m", "venv", "/opt/invariant-python"]
ENV PIP_CONFIG_FILE=/dev/null
RUN ["/opt/invariant-python/bin/python", "-I", "-m", "pip", "--isolated", "install", "--index-url", "https://pypi.org/simple", "--only-binary=:all:", "--ignore-installed", "--no-cache-dir", "--disable-pip-version-check", "--report", "` + pythonDepsReport + `", "-r", "/inputs/requirements.txt"]
ENV PATH=/opt/invariant-python/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
`

// PythonDependencies identifies an application's environment separately from
// Nagini. Image is selected once for execution; the portable evidence is the
// recipe, its inputs and the wheels actually installed, including transitives.
type PythonDependencies struct {
	Image     string           `json:"-"`
	Base      string           `json:"base"`
	Recipe    string           `json:"recipe"`
	Platform  string           `json:"platform"`
	Inputs    []PythonInput    `json:"inputs"`
	Artifacts []PythonArtifact `json:"artifacts"`
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

var (
	pythonDigest         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonName           = `[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?`
	pythonVersion        = `(?:===|==|!=|~=|<=|>=|<|>)[ \t]*[A-Za-z0-9.*+!_-]+`
	pythonRequirement    = regexp.MustCompile(`^` + pythonName + `(?:[ \t]*\[[ \t]*` + pythonName + `(?:[ \t]*,[ \t]*` + pythonName + `)*[ \t]*\])?[ \t]*(?:\(?[ \t]*` + pythonVersion + `(?:[ \t]*,[ \t]*` + pythonVersion + `)*[ \t]*\)?)?[ \t]*$`)
	pythonComment        = regexp.MustCompile(`(^|[ \t]+)#.*$`)
	pythonEncoding       = regexp.MustCompile(`coding[:=][ \t]*([-\w.]+)`)
	pythonNormalizedName = regexp.MustCompile(`[-_.]+`)
	pythonPackageName    = regexp.MustCompile(`^` + pythonName + `$`)
	pythonWheel          = regexp.MustCompile(`^[A-Za-z0-9_.+!-]+\.whl$`)
)

// PythonDepsRecipe reads and validates the complete requirement/constraint
// closure without invoking Docker. No requirements.txt means no environment.
func PythonDepsRecipe(root string) (string, error) {
	inputs, err := pythonInputs(root)
	if err != nil || inputs == nil {
		return "", err
	}
	return pythonRecipe(inputs).Recipe, nil
}

func pythonHash(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func pythonRecipe(inputs map[string][]byte) *PythonDependencies {
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform}
	var names []string
	for name := range inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	write := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	write([]byte(pythonDepsPlatform))
	write([]byte(pythonDepsDockerfile))
	for _, name := range names {
		write([]byte(name))
		write(inputs[name])
		env.Inputs = append(env.Inputs, PythonInput{Path: name, SHA256: pythonHash(inputs[name])})
	}
	env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return env
}

// pythonInputs holds one snapshot, used both for hashing and for staging.
// Every reference is walked before cleaning it, so a symlink followed by ..
// cannot change which bytes pip reads relative to the bytes the recipe names.
func pythonInputs(root string) (map[string][]byte, error) {
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	inputs, visiting := map[string][]byte{}, map[string]bool{}
	var read func(string, string) error
	read = func(from, ref string) error {
		name, err := pythonInputPath(root, from, ref)
		if err != nil {
			return fmt.Errorf("Python dependencies: %s references %q: %w", from, ref, err)
		}
		if visiting[name] {
			return fmt.Errorf("Python dependencies: requirement/constraint cycle at %s", name)
		}
		if _, ok := inputs[name]; ok {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		refs, err := pythonReferences(b)
		if err != nil {
			return fmt.Errorf("Python dependencies: %s: %w", name, err)
		}
		inputs[name], visiting[name] = b, true
		for _, ref := range refs {
			if err := read(name, ref); err != nil {
				return err
			}
		}
		visiting[name] = false
		return nil
	}
	if err := read("requirements.txt", "requirements.txt"); err != nil {
		return nil, err
	}
	return inputs, nil
}

func pythonInputPath(root, from, ref string) (string, error) {
	if ref == "" || path.IsAbs(ref) || strings.ContainsAny(ref, "\\:$\x00") {
		return "", fmt.Errorf("use a local requirement or constraint file inside the project")
	}
	name := path.Dir(from)
	parts := strings.Split(ref, "/")
	for i, part := range parts {
		name = path.Join(name, part)
		if name == ".." || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("path escapes the project")
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinked input %s is not supported", name)
		}
		if i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("input %s must be a regular file with directory parents", name)
		}
	}
	return name, nil
}

// pythonReferences checks the subset of pip input that the build accounts
// for. Pip still checks requirement syntax and hash consistency. In particular,
// reject Python's extra line separators before comments can hide an option.
func pythonReferences(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	if strings.ContainsAny(s, "\v\f\x1c\x1d\x1e\u0085\u2028\u2029\x00") {
		return nil, fmt.Errorf("requirements allow only CR and LF line separators and no NUL bytes")
	}
	if strings.Contains(s, "${") {
		return nil, fmt.Errorf("environment substitutions are not supported")
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	// Pip honors encoding declarations in its first two lines. A different
	// codec could turn apparently inert comment bytes into options or includes.
	lines := strings.Split(s, "\n")
	for _, line := range lines[:min(2, len(lines))] {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			if m := pythonEncoding.FindStringSubmatch(line); m != nil {
				encoding := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(m[1]))
				if encoding != "utf8" && encoding != "utf8sig" {
					return nil, fmt.Errorf("requirements must declare UTF-8, not %q", m[1])
				}
			}
		}
	}
	var refs []string
	pending := ""
	for _, physical := range lines {
		comment := strings.HasPrefix(strings.TrimSpace(physical), "#")
		if !comment && strings.HasSuffix(physical, "\\") {
			pending += strings.Trim(physical, "\\")
			continue
		}
		if comment {
			physical = " " + physical
		}
		line := strings.TrimSpace(pythonComment.ReplaceAllString(pending+physical, ""))
		pending = ""
		if line == "" {
			continue
		}
		ref, err := pythonRequirementLine(line)
		if err != nil {
			return nil, err
		}
		if ref != "" {
			refs = append(refs, ref)
		}
	}
	if pending != "" {
		return nil, fmt.Errorf("unfinished requirement continuation")
	}
	return refs, nil
}

func pythonRequirementLine(line string) (string, error) {
	unsupported := func() (string, error) {
		return "", fmt.Errorf("unsupported requirement %q: use PyPI wheel names, versions, extras, markers, SHA-256 hashes and local -r/-c files only", line)
	}
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		rest := line[len(option):]
		if len(option) > 2 && (rest == "" || rest[0] != '=' && rest[0] != ' ' && rest[0] != '\t') {
			return unsupported()
		}
		rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
		args, err := pythonOptionWords(rest)
		if err != nil || len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
			return unsupported()
		}
		return args[0], nil
	}
	// Pip splits options from requirement text before interpreting marker
	// quotes. Only hashes may follow a package requirement.
	end := len(line)
	for i := 0; i < len(line); i++ {
		if line[i] == '-' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			end = i
			break
		}
	}
	args, err := pythonOptionWords(line[end:])
	if err != nil {
		return unsupported()
	}
	for i := 0; i < len(args); i++ {
		value, ok := strings.CutPrefix(args[i], "--hash=")
		if args[i] == "--hash" && i+1 < len(args) {
			i++
			value, ok = args[i], true
		}
		if !ok || !pythonDigest.MatchString(strings.ToLower(value)) {
			return unsupported()
		}
	}
	requirement, _, _ := strings.Cut(strings.TrimSpace(line[:end]), ";")
	if !pythonRequirement.MatchString(strings.TrimSpace(requirement)) || strings.ContainsAny(line[:end], "\\$") {
		return unsupported()
	}
	// Bare archive filenames can look like package names to the grammar, but
	// pip interprets them as local distributions.
	name := strings.ToLower(strings.FieldsFunc(requirement, func(r rune) bool { return strings.ContainsRune("[ (<>=!~\t", r) })[0])
	for _, suffix := range []string{".whl", ".zip", ".tar", ".gz", ".tgz", ".bz2", ".tbz", ".xz", ".txz", ".lz", ".tlz", ".lzma", ".z"} {
		if strings.HasSuffix(name, suffix) {
			return unsupported()
		}
	}
	return "", nil
}

// Options use pip's shell-like quoting, without expansions or escapes. The
// original bytes, including the quotes, are always what pip receives.
func pythonOptionWords(s string) ([]string, error) {
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	for _, c := range s {
		switch {
		case c == '\\':
			return nil, fmt.Errorf("escaped options are not supported")
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, started = c, true
		case c == ' ' || c == '\t':
			if started {
				words = append(words, word.String())
				word.Reset()
				started = false
			}
		default:
			started = true
			word.WriteRune(c)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if started {
		words = append(words, word.String())
	}
	return words, nil
}

// PythonDepsImage builds or reuses a recipe, then reads its actual resolution.
// A tag is only a cache key. A build's iidfile, or one inspection on a cache
// hit, fixes the immutable image used for evidence and all application runs.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := pythonInputs(root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := pythonRecipe(inputs)
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	id, inspectErr := Docker(ctx, "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if inspectErr != nil {
		work, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
		dir := filepath.Join(work, "context")
		for name, b := range inputs {
			target := filepath.Join(dir, "inputs", filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, b, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		iid := filepath.Join(work, "image.id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iid, "-t", tag, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies (PyPI wheels only): %w\n%s", err, out)
		}
		id, err = os.ReadFile(iid)
		if err != nil {
			return nil, fmt.Errorf("reading the built Python dependency image ID: %w", err)
		}
	}
	env.Image = strings.TrimSpace(string(id))
	if !pythonDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("Python dependencies: Docker returned an invalid immutable image ID %q", env.Image)
	}
	// The application can install commands named cat or sh in its venv.
	// Read evidence with the pinned base's cat, without consulting that PATH.
	report, err := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", "-w", "/build", env.Image, "/bin/cat", pythonDepsReport).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading the pip installation report from Python dependency image %s: %w\n%s", env.Image, err, report)
	}
	env.Artifacts, err = pythonArtifacts(report)
	if err != nil {
		return nil, fmt.Errorf("Python dependency image %s has invalid pip installation evidence: %w", env.Image, err)
	}
	return env, nil
}

func pythonArtifacts(b []byte) ([]PythonArtifact, error) {
	var report struct {
		Version string `json:"version"`
		Install []struct {
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
		return nil, err
	}
	if report.Version != "1" || report.Install == nil {
		return nil, fmt.Errorf("expected a version 1 pip report with an install list")
	}
	artifacts := make([]PythonArtifact, 0, len(report.Install))
	seen := map[string]bool{}
	for _, install := range report.Install {
		u, err := url.Parse(install.Download.URL)
		if err != nil || u.Scheme == "" || !pythonWheel.MatchString(path.Base(u.Path)) {
			return nil, fmt.Errorf("%s has no resolved wheel filename", install.Metadata.Name)
		}
		a := PythonArtifact{
			Name:    pythonNormalizedName.ReplaceAllString(strings.ToLower(install.Metadata.Name), "-"),
			Version: install.Metadata.Version, Filename: path.Base(u.Path),
			SHA256: "sha256:" + install.Download.Archive.Hashes["sha256"],
		}
		if !pythonPackageName.MatchString(a.Name) || strings.TrimSpace(a.Version) == "" ||
			strings.ContainsAny(a.Version, " \t\r\n\x00") || !pythonDigest.MatchString(a.SHA256) || seen[a.Name] {
			return nil, fmt.Errorf("incomplete or duplicate resolved wheel evidence for %q: need name, version and SHA-256", a.Name)
		}
		seen[a.Name] = true
		artifacts = append(artifacts, a)
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
