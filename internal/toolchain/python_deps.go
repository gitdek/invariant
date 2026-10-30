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

// PythonDependencies identifies the application's environment separately
// from Nagini. The local image ID is only an execution handle; receipts use
// the recipe, input hashes and the wheels actually installed in that image.
type PythonDependencies struct {
	Image string `json:"-"`

	Base     string `json:"base"`
	Recipe   string `json:"recipe"`
	Platform string `json:"platform"`

	Inputs    []PythonDependencyInput    `json:"inputs"`
	Artifacts []PythonDependencyArtifact `json:"artifacts"`
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

const pythonDepsPlatform = "linux/amd64"

// Inputs remain inert data, even when a referenced file is named pip.py or
// venv.py. COPY takes a directory, so Docker never expands input filenames.
// A fresh venv and --ignore-installed make the report cover every requested
// distribution, including any that also ships in the base image.
const pythonDepsDockerfile = `FROM --platform=linux/amd64 ` + PythonImage + `
COPY ["inputs/", "/opt/invariant/inputs/"]
WORKDIR /opt/invariant/build
ENV PIP_CONFIG_FILE=/dev/null
RUN ["python", "-I", "-m", "venv", "/opt/invariant/venv"]
RUN ["/opt/invariant/venv/bin/python", "-I", "-m", "pip", "--isolated", "install", "--disable-pip-version-check", "--no-cache-dir", "--only-binary=:all:", "--index-url", "https://pypi.org/simple", "--ignore-installed", "--report", "/opt/invariant/python-dependencies.json", "-r", "/opt/invariant/inputs/requirements.txt"]
ENV PATH=/opt/invariant/venv/bin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
`

type pythonDependencyFile struct {
	path string
	data []byte
	dirs []string // directories traversed by unchanged local references
}

// PythonDepsRecipe hashes the complete recipe and its recursive requirements
// and constraints, without invoking Docker or modifying the project.
func PythonDepsRecipe(root string) (string, error) {
	files, err := pythonDependencyFiles(root)
	if err != nil || len(files) == 0 {
		return "", err
	}
	return pythonDependencyRecipe(files), nil
}

func pythonDependencyRecipe(files []pythonDependencyFile) string {
	h := sha256.New()
	// Lengths delimit both paths and contents, independent of checkout location.
	write := func(b []byte) {
		fmt.Fprintf(h, "%d:", len(b))
		h.Write(b)
	}
	write([]byte(PythonImage))
	write([]byte(pythonDepsPlatform))
	write([]byte(pythonDepsDockerfile))
	for _, f := range files {
		write([]byte(f.path))
		write(f.data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// PythonDepsImage selects one immutable environment. Only building may use
// the network; evidence is always read from that image, including on reuse.
func PythonDepsImage(ctx context.Context, root string) (*PythonDependencies, error) {
	files, err := pythonDependencyFiles(root)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	env := &PythonDependencies{Base: PythonImage, Platform: pythonDepsPlatform, Recipe: pythonDependencyRecipe(files)}
	for _, f := range files {
		sum := sha256.Sum256(f.data)
		env.Inputs = append(env.Inputs, PythonDependencyInput{Path: f.path, SHA256: "sha256:" + hex.EncodeToString(sum[:])})
	}
	tag := "invariant-python-deps:" + strings.TrimPrefix(env.Recipe, "sha256:")
	out, err := Docker(ctx, "image", "inspect", tag).Output()
	if err == nil {
		var images []struct {
			ID           string `json:"Id"`
			OS           string `json:"Os"`
			Architecture string `json:"Architecture"`
		}
		if err := json.Unmarshal(out, &images); err != nil || len(images) != 1 {
			return nil, fmt.Errorf("selecting Python dependencies: invalid Docker image inspection for %s", tag)
		}
		if images[0].OS+"/"+images[0].Architecture != pythonDepsPlatform {
			return nil, fmt.Errorf("selecting Python dependencies: %s is not %s", tag, pythonDepsPlatform)
		}
		env.Image = images[0].ID
	} else {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		work, err := os.MkdirTemp("", "invariant-python-deps-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(work)
		contextDir := filepath.Join(work, "context")
		for _, f := range files {
			for _, dir := range f.dirs {
				if err := os.MkdirAll(filepath.Join(contextDir, "inputs", filepath.FromSlash(dir)), 0o755); err != nil {
					return nil, err
				}
			}
			target := filepath.Join(contextDir, "inputs", filepath.FromSlash(f.path))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(target, f.data, 0o644); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(pythonDepsDockerfile), 0o644); err != nil {
			return nil, err
		}
		// The build's own ID avoids selecting another builder's replacement tag.
		iidfile := filepath.Join(work, "image-id")
		out, err := Docker(ctx, "build", "--platform", pythonDepsPlatform, "--iidfile", iidfile, "-t", tag, contextDir).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("installing Python dependencies: %w\n%s", err, strings.TrimSpace(string(out)))
		}
		id, err := os.ReadFile(iidfile)
		if err != nil {
			return nil, fmt.Errorf("selecting built Python dependencies: %w", err)
		}
		env.Image = strings.TrimSpace(string(id))
	}
	if !pythonDependencyDigest.MatchString(env.Image) {
		return nil, fmt.Errorf("selecting Python dependencies: invalid immutable image ID %q", env.Image)
	}
	out, err = Docker(ctx, "run", "--rm", "--network", "none", "--platform", pythonDepsPlatform, "--read-only", env.Image,
		"cat", "/opt/invariant/python-dependencies.json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading Python dependency evidence from %s: %w\n%s", env.Image, err, strings.TrimSpace(string(out)))
	}
	env.Artifacts, err = pythonDependencyArtifacts(out)
	if err != nil {
		return nil, fmt.Errorf("Python dependency evidence from %s: %w", env.Image, err)
	}
	return env, nil
}

var (
	pythonDependencyDigest      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonRequirement           = regexp.MustCompile(`(?i)^([a-z0-9](?:[a-z0-9._-]*[a-z0-9])?)(?:[ \t]*\[[a-z0-9._, \t-]+\])?[ \t]*([<>=!~(][a-z0-9._*+!~<>=(), \t-]*)?$`)
	pythonRequirementOption     = regexp.MustCompile(`(?:^|[ \t])-[^ \t]+`)
	pythonNextRequirementOption = regexp.MustCompile(`[ \t]+-`)
	pythonRequirementHash       = regexp.MustCompile(`^--hash(?:=|[ \t]+)sha256:[a-fA-F0-9]{64}$`)
	pythonPackageName           = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9._-]*[a-z0-9])?$`)
	pythonNameSeparators        = regexp.MustCompile(`[-_.]+`)
	pythonEncoding              = regexp.MustCompile(`coding[:=][ \t]*([-\w.]+)`)
)

func pythonDependencyFiles(root string) ([]pythonDependencyFile, error) {
	if _, err := os.Lstat(filepath.Join(root, "requirements.txt")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Python requirements.txt: %w", err)
	}
	files := map[string]pythonDependencyFile{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(filename string) error {
		if visiting[filename] {
			return fmt.Errorf("Python requirements: reference cycle at %s", filename)
		}
		if _, seen := files[filename]; seen {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
		if err != nil {
			return fmt.Errorf("Python requirements %s: %w", filename, err)
		}
		if !utf8.Valid(data) {
			return fmt.Errorf("Python requirements %s must be UTF-8", filename)
		}
		// pip uses str.splitlines before removing comments or joining lines.
		// Reject its other separators here, so a comment cannot hide options.
		for _, r := range string(data) {
			if strings.ContainsRune("\v\f\u001c\u001d\u001e\u0085\u2028\u2029", r) || r == 0 {
				return fmt.Errorf("Python requirements %s: unsupported line separator or control character U+%04X", filename, r)
			}
		}
		visiting[filename] = true
		dirs := map[string]bool{}
		lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n"), "\r", "\n"), "\n")
		for i, line := range lines {
			if i < 2 && strings.HasPrefix(strings.TrimSpace(line), "#") {
				if m := pythonEncoding.FindStringSubmatch(line); m != nil && m[1] != "utf-8" && m[1] != "utf8" && m[1] != "utf_8" {
					return fmt.Errorf("Python requirements %s: only UTF-8 encoding is supported", filename)
				}
			}
		}
		var joined string
		check := func(line string) error {
			line = strings.TrimSpace(pythonRequirementComment(line))
			if line == "" {
				return nil
			}
			ref, err := pythonRequirementReference(line)
			if err != nil {
				return fmt.Errorf("Python requirements %s: %w", filename, err)
			}
			if ref != "" {
				next, err := pythonDependencyPath(root, path.Dir(filename), ref, dirs)
				if err != nil {
					return fmt.Errorf("Python requirements %s references %q: %w", filename, ref, err)
				}
				return visit(next)
			}
			return nil
		}
		for _, line := range lines {
			comment := strings.HasPrefix(strings.TrimSpace(line), "#")
			if strings.HasSuffix(line, "\\") && !comment {
				joined += strings.Trim(line, "\\")
				continue
			}
			if comment && joined != "" {
				line = " " + line
			}
			if err := check(joined + line); err != nil {
				return err
			}
			joined = ""
		}
		if err := check(joined); err != nil {
			return err
		}
		visiting[filename] = false
		f := pythonDependencyFile{path: filename, data: data}
		for dir := range dirs {
			f.dirs = append(f.dirs, dir)
		}
		sort.Strings(f.dirs)
		files[filename] = f
		return nil
	}
	filename, err := pythonDependencyPath(root, ".", "requirements.txt", nil)
	if err != nil {
		return nil, fmt.Errorf("Python requirements.txt: %w", err)
	}
	if err := visit(filename); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]pythonDependencyFile, 0, len(names))
	for _, name := range names {
		result = append(result, files[name])
	}
	return result, nil
}

// Check each path component before cleaning it, including components later
// followed by ..: a symlink there would change which file pip reads.
func pythonDependencyPath(root, dir, ref string, dirs map[string]bool) (string, error) {
	if path.IsAbs(ref) || strings.ContainsAny(ref, "\\:$\x00") {
		return "", fmt.Errorf("expected a local relative requirement or constraint file")
	}
	parts := strings.Split(ref, "/")
	for i, part := range parts {
		switch part {
		case "..":
			if dir == "." {
				return "", fmt.Errorf("reference escapes the project")
			}
			dir = path.Dir(dir)
		case ".", "":
		default:
			dir = path.Join(dir, part)
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlinked input %s", dir)
		}
		if i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("expected a regular input file, got %s", dir)
		}
		if info.IsDir() && dir != "." {
			dirs[dir] = true
		}
	}
	return dir, nil
}

func pythonRequirementComment(line string) string {
	var previous rune
	for i, r := range line {
		if r == '#' && (i == 0 || unicode.IsSpace(previous)) {
			return line[:i]
		}
		previous = r
	}
	return line
}

// This is a source-policy check, not a replacement for pip's requirement
// parser. pip still validates versions, markers and complete hash rules.
func pythonRequirementReference(line string) (string, error) {
	if strings.Contains(line, "${") || strings.ContainsRune(line, '\\') {
		return "", fmt.Errorf("environment substitutions and escapes are unsupported")
	}
	for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
		if !strings.HasPrefix(line, option) {
			continue
		}
		rest := strings.TrimPrefix(line, option)
		if len(option) > 2 && rest != "" && rest[0] != '=' && rest[0] != ' ' && rest[0] != '\t' {
			continue
		}
		if len(option) > 2 {
			rest = strings.TrimPrefix(rest, "=")
		}
		rest = strings.TrimSpace(rest)
		if len(rest) >= 2 && (rest[0] == '\'' || rest[0] == '"') && rest[len(rest)-1] == rest[0] {
			rest = rest[1 : len(rest)-1]
		} else if strings.ContainsAny(rest, " \t") {
			return "", fmt.Errorf("each requirement or constraint reference must name one local file")
		}
		if rest == "" || strings.ContainsAny(rest, "\"'") || strings.HasPrefix(rest, "-") {
			return "", fmt.Errorf("invalid requirement or constraint reference")
		}
		return rest, nil
	}
	// Options can also follow a requirement. Only pip's artifact hashes are
	// permitted; none may change the index, source policy or recursive inputs.
	if loc := pythonRequirementOption.FindStringIndex(line); loc != nil {
		options := strings.TrimSpace(line[loc[0]:])
		line = strings.TrimSpace(line[:loc[0]])
		for options != "" {
			next := pythonNextRequirementOption.FindStringIndex(options)
			end := len(options)
			if next != nil {
				end = next[0]
			}
			if !pythonRequirementHash.MatchString(strings.TrimSpace(options[:end])) {
				return "", fmt.Errorf("only PyPI wheel requirements, local -r/-c references and --hash=sha256:... are supported")
			}
			options = strings.TrimSpace(options[end:])
		}
	}
	requirement, _, _ := strings.Cut(line, ";")
	m := pythonRequirement.FindStringSubmatch(strings.TrimSpace(requirement))
	if m == nil {
		return "", fmt.Errorf("unsupported requirement %q: use PyPI wheel names, versions, extras and markers", line)
	}
	for _, suffix := range []string{".whl", ".zip", ".tar", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.lz", ".tar.lzma", ".tgz", ".tbz", ".txz", ".tlz"} {
		if strings.HasSuffix(strings.ToLower(m[1]), suffix) {
			return "", fmt.Errorf("local packages and source archives are unsupported: %s", m[1])
		}
	}
	return "", nil
}

func pythonDependencyArtifacts(data []byte) ([]PythonDependencyArtifact, error) {
	type archive struct {
		Hashes map[string]string `json:"hashes"`
	}
	type download struct {
		URL     string  `json:"url"`
		Archive archive `json:"archive_info"`
	}
	type metadata struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type installation struct {
		Download download `json:"download_info"`
		Direct   bool     `json:"is_direct"`
		Metadata metadata `json:"metadata"`
	}
	var report struct {
		Version string          `json:"version"`
		Install *[]installation `json:"install"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("invalid pip installation report: %w", err)
	}
	if report.Version != "1" || report.Install == nil {
		return nil, fmt.Errorf("missing or unsupported pip installation report")
	}
	artifacts := make([]PythonDependencyArtifact, 0, len(*report.Install))
	seen := map[string]bool{}
	for _, entry := range *report.Install {
		u, err := url.Parse(entry.Download.URL)
		if err != nil || u.Scheme != "https" || u.Host != "files.pythonhosted.org" || u.User != nil || entry.Direct {
			return nil, fmt.Errorf("pip report contains an artifact outside public PyPI")
		}
		filename := path.Base(u.Path)
		digest := "sha256:" + entry.Download.Archive.Hashes["sha256"]
		if !strings.HasSuffix(filename, ".whl") || !pythonDependencyDigest.MatchString(digest) ||
			!pythonPackageName.MatchString(entry.Metadata.Name) || strings.TrimSpace(entry.Metadata.Version) == "" {
			return nil, fmt.Errorf("pip report lacks wheel filename, package metadata or SHA-256 for %q", entry.Metadata.Name)
		}
		name := pythonNameSeparators.ReplaceAllString(strings.ToLower(entry.Metadata.Name), "-")
		if seen[name] {
			return nil, fmt.Errorf("pip report contains duplicate distribution %s", name)
		}
		seen[name] = true
		artifacts = append(artifacts, PythonDependencyArtifact{Name: name, Version: entry.Metadata.Version, Filename: filename, SHA256: digest})
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
