package verify

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

//go:embed python_application_runner.py
var pythonApplicationRunner []byte

type PythonApplicationInvocationKey struct {
	Target string `json:"target"`
	Check  string `json:"check"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
}

type PythonApplicationInvocation struct {
	Key      PythonApplicationInvocationKey      `json:"key"`
	Declared project.ResolvedComponentInvocation `json:"declared"`
	Sampling map[string]string                   `json:"sampling"`
}

type PythonApplicationProvenance struct {
	Files        []project.ComponentFile       `json:"files"`
	Dependencies *toolchain.PythonDependencies `json:"dependencies,omitempty"`
	RunnerSHA256 string                        `json:"runner_sha256"`
	Fingerprint  string                        `json:"fingerprint"`
}

type PythonApplicationSessionOptions struct {
	SelectImage func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error)
	Execute     func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error)
}

// PythonApplicationSession owns runtime resources, not the caller's staged tree.
// Runs are serialized; Close waits for an active run and prevents later runs.
type PythonApplicationSession struct {
	mu                           sync.Mutex
	closed                       bool
	root, private, image, driver string
	invocations                  []PythonApplicationInvocation
	declarations                 map[string]project.Component
	provenance                   PythonApplicationProvenance
	snapshot                     map[string]pythonApplicationFile
	execute                      func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error)
}

type pythonApplicationFile struct {
	info os.FileInfo
	hash string
}

var (
	pythonApplicationHash         = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	pythonApplicationName         = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*(\.[A-Za-z_][A-Za-z_0-9]*)*$`)
	pythonApplicationEnv          = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
	pythonApplicationDecimal      = regexp.MustCompile(`^[0-9]+$`)
	pythonApplicationDistribution = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	pythonApplicationNormalize    = regexp.MustCompile(`[-_.]+`)
)

func pythonApplicationDigest(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func pythonApplicationFingerprint(v any) string {
	b, _ := json.Marshal(v)
	return pythonApplicationDigest(b)
}

func clonePythonApplicationInvocation(v PythonApplicationInvocation) PythonApplicationInvocation {
	v.Declared.Args = slices.Clone(v.Declared.Args)
	v.Declared.Env = maps.Clone(v.Declared.Env)
	v.Declared.Sampling = maps.Clone(v.Declared.Sampling)
	v.Declared.Inputs = slices.Clone(v.Declared.Inputs)
	v.Sampling = maps.Clone(v.Sampling)
	return v
}

func clonePythonApplicationProvenance(p PythonApplicationProvenance) PythonApplicationProvenance {
	p.Files = slices.Clone(p.Files)
	if p.Dependencies != nil {
		d := *p.Dependencies
		d.Inputs = slices.Clone(d.Inputs)
		d.CanonicalInputs = slices.Clone(d.CanonicalInputs)
		d.Artifacts = slices.Clone(d.Artifacts)
		p.Dependencies = &d
	}
	return p
}

func (s *PythonApplicationSession) Invocations() []PythonApplicationInvocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PythonApplicationInvocation, len(s.invocations))
	for i, v := range s.invocations {
		out[i] = clonePythonApplicationInvocation(v)
	}
	return out
}

func (s *PythonApplicationSession) Provenance() PythonApplicationProvenance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clonePythonApplicationProvenance(s.provenance)
}

func (s *PythonApplicationSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return os.RemoveAll(s.private)
}

// PreparePythonApplicationSession validates every declaration before selecting
// an image. dst follows StagePythonApplication's ownership and failure rules.
func PreparePythonApplicationSession(ctx context.Context, p *project.Project, dst string, options PythonApplicationSessionOptions) (*PythonApplicationSession, error) {
	if p == nil {
		return nil, fmt.Errorf("Python application project is required")
	}
	w, err := StagePythonApplication(p, dst)
	if err != nil {
		return nil, err
	}
	if w.Inventory.Mode != "explicit" {
		return nil, fmt.Errorf("Python application session requires explicit components")
	}
	s := &PythonApplicationSession{root: w.Root, declarations: map[string]project.Component{}, execute: options.Execute}
	if s.execute == nil {
		s.execute = RunPythonApplicationCommand
	}
	rel, err := filepath.Rel(w.Root, w.Project.Dir)
	if err != nil {
		return nil, err
	}
	s.driver = path.Join(filepath.ToSlash(rel), w.Project.Manifest.Conformance)
	for _, c := range w.Inventory.Provenance.Declarations {
		s.declarations[c.ID] = c
	}
	for _, c := range w.Inventory.Checks {
		for _, d := range c.Invocations {
			v := PythonApplicationInvocation{Key: PythonApplicationInvocationKey{c.Target, d.Check, d.Kind, d.ID}, Declared: d}
			if err := s.validate(&v); err != nil {
				return nil, fmt.Errorf("Python invocation %q: %w", d.ID, err)
			}
			s.invocations = append(s.invocations, clonePythonApplicationInvocation(v))
		}
	}
	s.snapshot, err = pythonApplicationSnapshot(s.root)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(pythonApplicationFiles(s.snapshot), w.Files) {
		return nil, fmt.Errorf("Python snapshot changed during preparation")
	}
	s.provenance = PythonApplicationProvenance{Files: slices.Clone(w.Files), RunnerSHA256: pythonApplicationDigest(pythonApplicationRunner)}
	selectImage := options.SelectImage
	if selectImage == nil {
		selectImage = preparePythonApplicationImage
	}
	image, deps, err := selectImage(ctx, w)
	if err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	if err := validatePythonApplicationDependencies(s.root, image, deps, s.provenance.Files); err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.image = image
	s.provenance.Dependencies = deps
	s.provenance = clonePythonApplicationProvenance(s.provenance)
	s.provenance.Fingerprint = pythonApplicationFingerprint(s.provenance)
	s.private, err = os.MkdirTemp("", "invariant-python-session-")
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *PythonApplicationSession) validate(v *PythonApplicationInvocation) error {
	d := v.Declared
	if d.Command != "python" {
		return fmt.Errorf("command must be exactly python")
	}
	for _, a := range d.Args {
		if strings.ContainsRune(a, 0) || !utf8.ValidString(a) {
			return fmt.Errorf("invalid argument")
		}
	}
	for k, value := range d.Env {
		u := strings.ToUpper(k)
		if !pythonApplicationEnv.MatchString(k) || strings.ContainsRune(value, 0) || !utf8.ValidString(value) ||
			strings.HasPrefix(u, "PYTHON") || strings.HasPrefix(u, "INVARIANT_") || strings.HasPrefix(u, "LD_") || strings.HasPrefix(u, "DYLD_") || strings.HasSuffix(u, "_PROXY") ||
			u == "PATH" || u == "HOME" || u == "DRIVER" || u == "ENV" || u == "BASH_ENV" {
			return fmt.Errorf("reserved or invalid environment key/value %q", k)
		}
	}
	switch d.Kind {
	case "test":
		if len(d.Sampling) != 0 {
			return fmt.Errorf("tests cannot declare sampling")
		}
		if len(d.Args) < 3 || d.Args[0] != "-m" || d.Args[1] != "unittest" {
			return fmt.Errorf("expected -m unittest and explicit dotted selections")
		}
		for _, name := range d.Args[2:] {
			if name == "discover" || !pythonApplicationName.MatchString(name) {
				return fmt.Errorf("invalid unittest selection %q", name)
			}
		}
		v.Sampling = map[string]string{"runs": "1"}
	case "conformance":
		if len(d.Args) == 0 {
			return fmt.Errorf("missing driver")
		}
		arg := d.Args[0]
		if path.IsAbs(arg) || path.Clean(arg) != arg || strings.Contains(arg, "\\") {
			return fmt.Errorf("invalid driver path")
		}
		// Inspect each component before cleaning: an excursion or a symlink
		// cannot be hidden by a later '..'.
		current := filepath.Join(s.root, filepath.FromSlash(d.WorkingDir))
		for _, part := range strings.Split(arg, "/") {
			current = filepath.Join(current, part)
			rel, err := filepath.Rel(s.root, current)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("driver escapes application")
			}
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("invalid driver path")
			}
		}
		rel, _ := filepath.Rel(s.root, current)
		info, err := os.Lstat(current)
		if err != nil || !info.Mode().IsRegular() || filepath.ToSlash(rel) != s.driver || s.declarations[d.Check].Source != s.driver {
			return fmt.Errorf("driver must match manifest and selected conformance source")
		}
		v.Sampling = map[string]string{"runs": "1000", "steps": "40", "seed": "1"}
		for k, value := range d.Sampling {
			if _, ok := v.Sampling[k]; !ok {
				return fmt.Errorf("unknown sampling control %q", k)
			}
			n, err := strconv.ParseUint(value, 10, 31)
			if !pythonApplicationDecimal.MatchString(value) || err != nil || (n == 0 && k != "seed") {
				return fmt.Errorf("invalid sampling %s", k)
			}
			v.Sampling[k] = strconv.FormatUint(n, 10)
		}
	default:
		return fmt.Errorf("unsupported invocation kind %q", d.Kind)
	}
	return nil
}

func pythonApplicationSnapshot(root string) (map[string]pythonApplicationFile, error) {
	out := map[string]pythonApplicationFile{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("Python snapshot contains nonregular entry")
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		f := pythonApplicationFile{info: info}
		if !info.IsDir() {
			b, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			after, err := os.Lstat(name)
			if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime() != after.ModTime() {
				return fmt.Errorf("Python snapshot changed while reading")
			}
			f.hash = pythonApplicationDigest(b)
		}
		out[filepath.ToSlash(rel)] = f
		return nil
	})
	return out, err
}

func pythonApplicationFiles(snapshot map[string]pythonApplicationFile) []project.ComponentFile {
	var files []project.ComponentFile
	for name, f := range snapshot {
		if !f.info.IsDir() {
			files = append(files, project.ComponentFile{Path: name, SHA256: f.hash})
		}
	}
	slices.SortFunc(files, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	return files
}

func (s *PythonApplicationSession) fresh() error {
	now, err := pythonApplicationSnapshot(s.root)
	if err != nil {
		return fmt.Errorf("stale Python snapshot: %w", err)
	}
	if len(now) != len(s.snapshot) {
		return fmt.Errorf("stale Python snapshot membership")
	}
	for name, before := range s.snapshot {
		after, ok := now[name]
		if !ok || !os.SameFile(before.info, after.info) || before.info.Mode() != after.info.Mode() || before.info.ModTime() != after.info.ModTime() || before.hash != after.hash {
			return fmt.Errorf("stale Python snapshot: %s", name)
		}
	}
	return nil
}

func preparePythonApplicationImage(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
	image, deps, err := w.SelectImage(ctx)
	if err != nil || deps != nil {
		return image, deps, err
	}
	// SelectImage's standard-library fast path stays Docker-free. Only default
	// session preparation acquires that exact digest, never execution.
	out, err := toolchain.Docker(ctx, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image).Output()
	if err != nil || strings.TrimSpace(string(out)) != "linux/amd64" {
		out, err = toolchain.Docker(ctx, "pull", "--platform", "linux/amd64", image).CombinedOutput()
		if err != nil {
			return "", nil, fmt.Errorf("acquiring Python application base: %w: %s", err, out)
		}
	}
	return image, nil, nil
}

func validatePythonApplicationDependencies(root, image string, d *toolchain.PythonDependencies, files []project.ComponentFile) error {
	inputs := map[string]string{}
	for _, f := range files {
		inputs[f.Path] = f.SHA256
	}
	bad := func() error { return fmt.Errorf("missing or inconsistent Python dependency evidence") }
	if inputs["requirements.txt"] == "" {
		if image != toolchain.PythonImage || d != nil {
			return bad()
		}
		return nil
	}
	if !pythonApplicationHash.MatchString(image) || d == nil || d.Image != image || d.Base != toolchain.PythonImage || d.Platform != "linux/amd64" || !pythonApplicationHash.MatchString(d.Recipe) || len(d.Inputs) == 0 || len(d.CanonicalInputs) != 2 || d.Artifacts == nil {
		return bad()
	}
	seen := map[string]bool{}
	for _, f := range d.Inputs {
		if seen[f.Path] || !pythonApplicationHash.MatchString(f.SHA256) || inputs[f.Path] != f.SHA256 {
			return bad()
		}
		seen[f.Path] = true
	}
	if !seen["requirements.txt"] {
		return bad()
	}
	// The selector is the authority for pip's canonicalization and resolution,
	// but its original-input evidence must cover exactly the staged include
	// graph. This walks references only; it is not another requirement parser
	// or installer and never runs Docker for a supplied selector.
	want, err := pythonApplicationRequirements(root, inputs)
	if err != nil || !reflect.DeepEqual(seen, want) {
		return bad()
	}
	seen = map[string]bool{}
	for _, f := range d.CanonicalInputs {
		if seen[f.Path] || (f.Path != "requirements.txt" && f.Path != "constraints.txt") || !pythonApplicationHash.MatchString(f.SHA256) {
			return bad()
		}
		seen[f.Path] = true
	}
	seen = map[string]bool{}
	for _, a := range d.Artifacts {
		if !pythonApplicationDistribution.MatchString(a.Name) || seen[a.Name] || a.Version == "" || path.Base(a.Filename) != a.Filename || strings.ContainsAny(a.Filename, "\\ \t\r\n\x00") || !strings.HasSuffix(a.Filename, ".whl") || !pythonApplicationHash.MatchString(a.SHA256) {
			return bad()
		}
		parts := strings.Split(strings.TrimSuffix(a.Filename, ".whl"), "-")
		if (len(parts) != 5 && len(parts) != 6) || pythonApplicationNormalize.ReplaceAllString(strings.ToLower(parts[0]), "-") != a.Name || parts[1] != a.Version {
			return bad()
		}
		for _, part := range parts {
			if part == "" {
				return bad()
			}
		}
		seen[a.Name] = true
	}
	return nil
}

func pythonApplicationRequirements(root string, files map[string]string) (map[string]bool, error) {
	seen, active := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if active[name] || files[name] == "" {
			return fmt.Errorf("invalid requirements reference")
		}
		if seen[name] {
			return nil
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || pythonApplicationDigest(b) != files[name] {
			return fmt.Errorf("stale requirements input")
		}
		seen[name], active[name] = true, true
		defer delete(active, name)
		text := strings.TrimPrefix(string(b), "\ufeff")
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\\\n", "")
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			for _, delimiter := range []string{" #", "\t#"} {
				if at := strings.Index(line, delimiter); at >= 0 {
					line = strings.TrimSpace(line[:at])
				}
			}
			for _, option := range []string{"--requirement", "--constraint", "-r", "-c"} {
				if !strings.HasPrefix(line, option) {
					continue
				}
				value := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, option), "="))
				if value == "" || path.IsAbs(value) || strings.Contains(value, "\\") {
					return fmt.Errorf("invalid requirements reference")
				}
				current := path.Dir(name)
				for _, part := range strings.Split(value, "/") {
					current = path.Join(current, part)
					if current == ".." || strings.HasPrefix(current, "../") {
						return fmt.Errorf("requirements reference escapes snapshot")
					}
					info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(current)))
					if err != nil || info.Mode()&os.ModeSymlink != 0 {
						return fmt.Errorf("invalid requirements reference")
					}
				}
				if err := visit(current); err != nil {
					return err
				}
				break
			}
		}
		return nil
	}
	err := visit("requirements.txt")
	return seen, err
}
