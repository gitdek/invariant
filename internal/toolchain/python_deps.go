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

// PythonDependencies records the application environment, independently of
// Nagini. A local image tag is only an execution detail, never evidence.
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

const (
	pythonDepsPlatform = "linux/amd64"
	pythonDepsReport   = "/opt/invariant-python-report.json"
	pythonDepsIgnore   = "# All referenced inputs belong to the build context.\n"
	pythonDepsInstall  = `RUN python -m venv /opt/invariant-python \
 && PIP_CONFIG_FILE=/dev/null /opt/invariant-python/bin/python -m pip --isolated install \
    --index-url https://pypi.org/simple --only-binary=:all: --ignore-installed \
    --no-cache-dir --disable-pip-version-check --no-input \
    --report ` + pythonDepsReport + ` -r requirements.txt
ENV PATH="/opt/invariant-python/bin:${PATH}"
WORKDIR /
`
)

type pythonDepsInputs struct {
	env        PythonDependencies
	files      map[string][]byte
	dirs       map[string]bool
	dockerfile string
	recipe     string
}

// PythonDepsRecipe hashes the complete installation recipe and the recursive
// requirements/constraints closure without running Docker or changing files.
// No requirements.txt means the project's plain Python image still suffices.
func PythonDepsRecipe(root string) (string, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return "", err
	}
	return inputs.env.Recipe, nil
}

// PythonDepsImage installs wheels from PyPI once per recipe. Only the build
// has network access. Both a new image and a reused one supply their own pip
// report; a recipe alone cannot identify a floating requirement's resolution.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	inputs, err := readPythonDeps(root)
	if err != nil || inputs == nil {
		return nil, err
	}
	env := inputs.env
	env.Image = "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	if Docker(ctx, "image", "inspect", env.Image).Run() != nil {
		dir, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		for name, b := range inputs.files {
			file := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(file, b, 0o644); err != nil {
				return nil, err
			}
		}
		dockerfile := filepath.Join(dir, inputs.dockerfile)
		if err := os.WriteFile(dockerfile, []byte(inputs.recipe), 0o644); err != nil {
			return nil, err
		}
		// A referenced .dockerignore must not change which inputs Docker sees.
		if err := os.WriteFile(dockerfile+".dockerignore", []byte(pythonDepsIgnore), 0o644); err != nil {
			return nil, err
		}
		// Evicting the tag requests a fresh resolution, even if build layers
		// remain. An existing tag, in contrast, keeps its actual resolution.
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--no-cache", "-f", dockerfile, "-t", env.Image, dir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python application dependencies from requirements.txt (PyPI wheels only): %w\n%s", err, out)
		}
	}
	out, err := Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform,
		"--read-only", env.Image, "cat", pythonDepsReport).Output()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency installation report from %s: %w; remove this image and retry to rebuild its evidence", env.Image, err)
	}
	if env.Artifacts, err = pythonArtifacts(out); err != nil {
		return nil, fmt.Errorf("invalid Python dependency installation report in %s: %w; remove this image and retry to rebuild its evidence", env.Image, err)
	}
	return &env, nil
}

func readPythonDeps(root string) (*pythonDepsInputs, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if _, err := r.Lstat("requirements.txt"); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	inputs := &pythonDepsInputs{
		env:   PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform},
		files: map[string][]byte{},
		dirs:  map[string]bool{},
	}
	active := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if active[name] {
			return fmt.Errorf("Python requirements reference cycle at %s", name)
		}
		if _, seen := inputs.files[name]; seen {
			return nil
		}
		b, err := r.ReadFile(filepath.FromSlash(name))
		if err != nil {
			return fmt.Errorf("Python requirements %s: %w", name, err)
		}
		refs, err := pythonReferences(b)
		if err != nil {
			return fmt.Errorf("Python requirements %s: %w", name, err)
		}
		inputs.files[name], active[name] = b, true
		for _, ref := range refs {
			next, err := pythonInputPath(r, path.Dir(name), ref, inputs.dirs)
			if err != nil {
				return fmt.Errorf("Python requirements %s references %q: %w", name, ref, err)
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		delete(active, name)
		return nil
	}
	name, err := pythonInputPath(r, ".", "requirements.txt", inputs.dirs)
	if err != nil {
		return nil, fmt.Errorf("Python requirements.txt: %w", err)
	}
	if err := visit(name); err != nil {
		return nil, err
	}
	for name, b := range inputs.files {
		inputs.env.Inputs = append(inputs.env.Inputs, PythonDependencyInput{Path: name, SHA256: pythonHash(b)})
	}
	sort.Slice(inputs.env.Inputs, func(i, j int) bool { return inputs.env.Inputs[i].Path < inputs.env.Inputs[j].Path })
	// Keep generated build files separate even when an input itself is named
	// Dockerfile. An explicit COPY also handles spaces without shell quoting.
	inputs.dockerfile = "Dockerfile"
	for pythonBuildCollision(inputs.files, inputs.dockerfile) || pythonBuildCollision(inputs.files, inputs.dockerfile+".dockerignore") {
		inputs.dockerfile += ".invariant"
	}
	var recipe strings.Builder
	recipe.WriteString("FROM " + PythonImage + "\nWORKDIR /invariant/requirements\n")
	// A reference such as deps/../shared.txt still traverses deps in pip.
	// Preserve those directories even if none of their files is an input.
	var dirs []string
	for dir := range inputs.dirs {
		dirs = append(dirs, "./"+dir)
	}
	sort.Strings(dirs)
	if len(dirs) > 0 {
		mkdir, _ := json.Marshal(append([]string{"mkdir", "-p", "--"}, dirs...))
		fmt.Fprintf(&recipe, "RUN %s\n", mkdir)
	}
	for _, input := range inputs.env.Inputs {
		copy, _ := json.Marshal([]string{input.Path, "./" + input.Path})
		fmt.Fprintf(&recipe, "COPY %s\n", copy)
	}
	recipe.WriteString(pythonDepsInstall)
	inputs.recipe = recipe.String()
	h := sha256.New()
	add := func(b []byte) {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	for _, s := range []string{"python-dependencies-v1; build --no-cache", PythonImage, pythonDepsPlatform, inputs.dockerfile, inputs.recipe, pythonDepsIgnore} {
		add([]byte(s))
	}
	for _, input := range inputs.env.Inputs {
		add([]byte(input.Path))
		add(inputs.files[input.Path])
	}
	inputs.env.Recipe = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return inputs, nil
}

func pythonBuildCollision(files map[string][]byte, name string) bool {
	for file := range files {
		if file == name || strings.HasPrefix(file, name+"/") {
			return true
		}
	}
	return false
}

// Check each path component before cleaning it, so a symlink followed by ..
// cannot disappear from the check. OpenRoot also confines subsequent reads.
func pythonInputPath(root *os.Root, dir, ref string, dirs map[string]bool) (string, error) {
	if ref == "" || path.IsAbs(ref) || strings.ContainsAny(ref, "\\:$\x00\r\n") {
		return "", fmt.Errorf("expected a local relative requirement or constraint file")
	}
	var parts []string
	for _, part := range strings.Split(dir+"/"+ref, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("path escapes the project root")
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
		name := path.Join(append([]string{"."}, parts...)...)
		info, err := root.Lstat(filepath.FromSlash(name))
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinked dependency inputs are not supported")
		}
		if info.IsDir() && name != "." {
			dirs[name] = true
		}
	}
	name := strings.Join(parts, "/")
	info, err := root.Lstat(filepath.FromSlash(name))
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("dependency input is not a regular file")
	}
	return name, nil
}

const pythonPackageName = `[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?`
const pythonVersionSpec = `(?:===|~=|==|!=|<=|>=|<|>)[ \t]*[A-Za-z0-9][A-Za-z0-9.*+!_-]*`

var (
	pythonComment       = regexp.MustCompile(`(^|[ \t]+)#.*$`)
	pythonEncoding      = regexp.MustCompile(`coding[:=][ \t]*([-\w.]+)`)
	pythonInclude       = regexp.MustCompile(`^(?:--requirement|--constraint)(?:[ \t]+|=)(.+)$|^-[rc][ \t]*(.+)$`)
	pythonHashes        = regexp.MustCompile(`(?:[ \t]+--hash(?:=|[ \t]+)(?:sha256:[a-fA-F0-9]{64}|sha384:[a-fA-F0-9]{96}|sha512:[a-fA-F0-9]{128}))+[ \t]*$`)
	pythonRequirement   = regexp.MustCompile(`^(` + pythonPackageName + `)[ \t]*(?:\[[ \t]*` + pythonPackageName + `(?:[ \t]*,[ \t]*` + pythonPackageName + `)*[ \t]*\])?[ \t]*(?:\(?[ \t]*` + pythonVersionSpec + `(?:[ \t]*,[ \t]*` + pythonVersionSpec + `)*[ \t]*\)?)?[ \t]*$`)
	pythonMarkerToken   = regexp.MustCompile(`^(?:'[^'\r\n]*'|"[^"\r\n]*"|===|~=|==|!=|<=|>=|<|>|\(|\)|[A-Za-z_]+)`)
	pythonName          = regexp.MustCompile(`^` + pythonPackageName + `$`)
	pythonNormalizeName = regexp.MustCompile(`[-_.]+`)
	pythonSHA256        = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Parse enough of pip's file format to account for every input and disallow
// alternate sources/options. Pip still validates PEP 508 and hash semantics.
func pythonReferences(b []byte) ([]string, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("requirements must be UTF-8")
	}
	text := strings.TrimPrefix(string(b), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var refs []string
	pending := ""
	for i, line := range strings.Split(text, "\n") {
		if i < 2 && strings.HasPrefix(strings.TrimSpace(line), "#") {
			if m := pythonEncoding.FindStringSubmatch(line); m != nil && m[1] != "utf-8" && m[1] != "utf8" {
				return nil, fmt.Errorf("line %d: requirements must use UTF-8 encoding", i+1)
			}
		}
		if strings.HasSuffix(line, "\\") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			pending += strings.TrimSuffix(line, "\\")
			continue
		}
		line, pending = pending+line, ""
		line = strings.TrimSpace(pythonComment.ReplaceAllString(line, ""))
		if line == "" {
			continue
		}
		if strings.ContainsAny(line, "$\x00") {
			return nil, fmt.Errorf("line %d: environment substitutions are not supported", i+1)
		}
		if m := pythonInclude.FindStringSubmatch(line); m != nil {
			ref := strings.TrimSpace(m[1] + m[2])
			if len(ref) >= 2 && (ref[0] == '\'' || ref[0] == '"') && ref[len(ref)-1] == ref[0] {
				ref = ref[1 : len(ref)-1]
			} else if strings.ContainsAny(ref, " \t") {
				return nil, fmt.Errorf("line %d: a reference must name one local file", i+1)
			}
			if strings.ContainsAny(ref, "\"'\t") {
				return nil, fmt.Errorf("line %d: invalid requirement file reference", i+1)
			}
			refs = append(refs, ref)
			continue
		}
		line = pythonHashes.ReplaceAllString(line, "")
		req, marker, hasMarker := strings.Cut(line, ";")
		m := pythonRequirement.FindStringSubmatch(strings.TrimSpace(req))
		if m == nil || pythonArchiveName(m[1]) || (hasMarker && !pythonMarker(marker)) {
			return nil, fmt.Errorf("line %d: only PyPI wheel names, versions, extras, markers and pip hashes are supported; URLs, VCS, local/editable packages and pip option overrides are not", i+1)
		}
	}
	if pending != "" {
		return nil, fmt.Errorf("unfinished requirement line continuation")
	}
	return refs, nil
}

func pythonArchiveName(name string) bool {
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz", ".tar.xz", ".txz"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			return true
		}
	}
	return false
}

func pythonMarker(marker string) bool {
	if strings.TrimSpace(marker) == "" {
		return false
	}
	for marker = strings.TrimSpace(marker); marker != ""; marker = strings.TrimSpace(marker) {
		token := pythonMarkerToken.FindString(marker)
		if token == "" {
			return false
		}
		if token[0] >= 'a' && token[0] <= 'z' {
			switch token {
			case "python_version", "python_full_version", "os_name", "sys_platform", "platform_release", "platform_system", "platform_version", "platform_machine", "platform_python_implementation", "implementation_name", "implementation_version", "extra", "extras", "dependency_groups", "and", "or", "in", "not":
			default:
				return false
			}
		}
		marker = marker[len(token):]
	}
	return true
}

func pythonHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
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
			Direct   bool `json:"is_direct"`
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
	for _, item := range report.Install {
		u, err := url.Parse(item.Download.URL)
		if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || u.User != nil || item.Direct {
			return nil, fmt.Errorf("expected a wheel downloaded from public PyPI")
		}
		filename := path.Base(u.Path)
		hash := item.Download.Archive.Hashes["sha256"]
		if !pythonName.MatchString(item.Metadata.Name) || strings.TrimSpace(item.Metadata.Version) == "" ||
			!strings.HasSuffix(filename, ".whl") || strings.ContainsAny(filename, "\\\r\n") || !pythonSHA256.MatchString(hash) {
			return nil, fmt.Errorf("wheel %q needs a name, version, wheel filename and SHA-256 artifact hash", filename)
		}
		name := pythonNormalizeName.ReplaceAllString(strings.ToLower(item.Metadata.Name), "-")
		if seen[name] {
			return nil, fmt.Errorf("duplicate installed distribution %q", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonArtifact{Name: name, Version: item.Metadata.Version, Filename: filename, SHA256: "sha256:" + hash})
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
