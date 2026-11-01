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

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

//go:embed python_application_runner.py
var pythonApplicationRunner []byte

//go:embed python_application_observer.py
var pythonApplicationObserver []byte

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
	// Explored is a conformance driver an exhaustive manifest runs through
	// the gate's exploration harness, with runs and steps as its budgets
	// (#342). Otherwise the driver samples.
	Explored bool `json:"explored,omitempty"`
}

type PythonApplicationProvenance struct {
	Files          []project.ComponentFile       `json:"files"`
	Dependencies   *toolchain.PythonDependencies `json:"dependencies,omitempty"`
	RunnerSHA256   string                        `json:"runner_sha256"`
	ObserverSHA256 string                        `json:"observer_sha256"`
	Fingerprint    string                        `json:"fingerprint"`
}

type PythonApplicationSessionOptions struct {
	SelectImage func(context.Context, *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error)
	Execute     func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error)
	// Nagini runs a selected function's verifier input (#353); without it,
	// the pinned Nagini runs in its own image. Distributions maps the
	// dependency image's top-level modules to the distributions that
	// installed them; without it, they're read in the image, offline.
	Nagini        func(context.Context, PythonApplicationNaginiCommand) (PythonApplicationProcess, error)
	Distributions func(ctx context.Context, image string) (map[string][]string, error)
}

// PythonApplicationSession owns immutable declarations and private runtime
// resources. The caller owns the staged destination, including on failure.
type PythonApplicationSession struct {
	project                      *project.Project
	inventory                    *project.ComponentInventory
	legacy                       bool
	targets, drivers             []project.Component
	delegates                    map[string][]string
	mu                           sync.Mutex
	closed                       bool
	root, runtime, image, driver string
	invocations                  []PythonApplicationInvocation
	provenance                   PythonApplicationProvenance
	declarations                 map[string]project.Component
	// contracts holds each trusted contract by its declaration's ID, the
	// target of its backing tests' invocation keys.
	contracts     map[string]project.ResolvedComponentContract
	snapshot      map[string]pythonApplicationFile
	execute       func(context.Context, PythonApplicationCommand) (PythonApplicationProcess, error)
	nagini        func(context.Context, PythonApplicationNaginiCommand) (PythonApplicationProcess, error)
	distributions func(context.Context, string) (map[string][]string, error)
	modules       map[string][]string
}

type pythonApplicationFile struct {
	info os.FileInfo
	hash string
}

var pythonApplicationDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var pythonApplicationName = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*(\.[A-Za-z_][A-Za-z_0-9]*)*$`)
var pythonApplicationEnv = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*$`)
var pythonApplicationDecimal = regexp.MustCompile(`^[0-9]+$`)
var pythonApplicationDistribution = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var pythonApplicationVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.!+_]*$`)

// pythonApplicationWheelName normalizes the distribution part of a wheel's
// filename as the installer does: runs of -, _ and . become -.
var pythonApplicationWheelName = regexp.MustCompile(`[-_.]+`)

func pythonApplicationHash(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func pythonApplicationFingerprint(v any) string {
	b, _ := json.Marshal(v)
	return pythonApplicationHash(b)
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
		if d.Script != nil {
			script := *d.Script
			d.Script = &script
		}
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
	return os.RemoveAll(s.runtime)
}

func PreparePythonApplicationSession(ctx context.Context, p *project.Project, dst string, options PythonApplicationSessionOptions) (*PythonApplicationSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("Python application project is required")
	}
	// Validate the caller's declarations as well as the reloaded staged ones.
	inv, err := project.ResolveComponents(p)
	if err != nil {
		return nil, err
	}
	app, err := project.ResolvePythonApplication(p)
	if err != nil {
		return nil, err
	}
	if _, err := pythonApplicationInvocations(p, app.Root, inv); err != nil {
		return nil, err
	}
	w, err := StagePythonApplication(p, dst)
	if err != nil {
		return nil, err
	}
	prepared, err := pythonApplicationInvocations(w.Project, w.Root, w.Inventory)
	if err != nil {
		return nil, err
	}
	snapshot, files, err := pythonApplicationSnapshot(w.Root)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(files, w.Files) {
		return nil, fmt.Errorf("Python application changed during staging")
	}
	s := &PythonApplicationSession{root: w.Root, snapshot: snapshot, invocations: prepared,
		declarations: map[string]project.Component{}, contracts: map[string]project.ResolvedComponentContract{}, execute: options.Execute, nagini: options.Nagini, distributions: options.Distributions}
	for _, c := range w.Inventory.TrustedContracts {
		s.contracts[c.Contract] = c
	}
	s.project, s.inventory = w.Project, w.Inventory
	s.delegates = map[string][]string{}
	s.legacy = w.Inventory.Mode == "legacy"
	declarations := slices.Clone(w.Inventory.Components)
	if s.legacy {
		s.targets, s.drivers, err = pythonApplicationLegacyBindings(w.Project, w.Root)
		if err != nil {
			return nil, err
		}
		declarations = append(declarations, s.targets...)
		declarations = append(declarations, s.drivers...)
	} else {
		declarations = append(declarations, w.Inventory.Provenance.Declarations...)
	}
	for _, c := range declarations {
		if f, ok := snapshot[c.Source]; !ok || f.hash != c.SourceSHA256 {
			return nil, fmt.Errorf("stale declaration %s", c.ID)
		}
		s.declarations[c.ID] = c
	}
	if !s.legacy {
		for _, check := range w.Inventory.Checks {
			paths, err := pythonApplicationTargetSources(w.Project, w.Root, s.declarations[check.Target])
			if err != nil {
				return nil, err
			}
			s.delegates[check.Target] = paths
		}
	}
	rel, err := filepath.Rel(w.Root, w.Project.Dir)
	if err != nil {
		return nil, err
	}
	s.driver = path.Join(filepath.ToSlash(rel), w.Project.Manifest.Conformance)
	s.provenance = PythonApplicationProvenance{Files: files, RunnerSHA256: pythonApplicationHash(pythonApplicationRunner), ObserverSHA256: pythonApplicationHash(pythonApplicationObserver)}
	selectImage := options.SelectImage
	if selectImage == nil {
		selectImage = selectPythonApplicationImage
	}
	image, deps, err := selectImage(ctx, w)
	if err != nil {
		return nil, err
	}
	if err := s.fresh(); err != nil {
		return nil, err
	}
	if err := pythonApplicationDependencies(files, w.Project.Manifest.Environment, image, deps); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.image = image
	s.provenance.Dependencies = deps
	s.provenance = clonePythonApplicationProvenance(s.provenance)
	s.provenance.Fingerprint = pythonApplicationFingerprint(s.provenance)
	s.runtime, err = os.MkdirTemp("", "invariant-python-session-")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.runtime, "runner.py"), pythonApplicationRunner, 0o644); err != nil {
		s.Close()
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.runtime, "invariant_observe.py"), pythonApplicationObserver, 0o644); err != nil {
		s.Close()
		return nil, err
	}
	if s.execute == nil {
		s.execute = RunPythonApplicationCommand
	}
	return s, nil
}

func pythonApplicationInvocations(p *project.Project, root string, inv *project.ComponentInventory) ([]PythonApplicationInvocation, error) {
	if inv.Mode == "legacy" {
		return pythonApplicationLegacyInvocations(p, root)
	}
	if inv.Mode != "explicit" || inv.Provenance == nil {
		return nil, fmt.Errorf("Python application sessions require explicit components")
	}
	decls := map[string]project.Component{}
	for _, c := range inv.Provenance.Declarations {
		decls[c.ID] = c
	}
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, err
	}
	driver, err := filepath.Rel(root, filepath.Join(dir, p.Manifest.Conformance))
	if err != nil {
		return nil, err
	}
	var out []PythonApplicationInvocation
	for _, check := range inv.Checks {
		var target project.Component
		for _, c := range inv.Components {
			if c.ID == check.Target {
				target = c
			}
		}
		// A session admits a proof selection in the form in-place proof
		// supports, for PythonApplicationSession.Prove; the gate itself
		// refuses it until #294's step 6 connects proof (#353).
		if check.Proof != nil {
			if err := pythonApplicationProofCheck(check, target); err != nil {
				return nil, err
			}
		}
		if _, err := pythonApplicationTargetSources(p, root, target); err != nil {
			return nil, err
		}
		for _, group := range []struct {
			kind string
			ids  []string
		}{{"test", check.Tests}, {"conformance", check.Conformance}} {
			for _, id := range group.ids {
				if !slices.ContainsFunc(check.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Kind == group.kind && v.Check == id }) {
					return nil, fmt.Errorf("selected %s has no executable invocation", group.kind)
				}
			}
		}
		for _, v := range check.Invocations {
			if err := pythonApplicationCommandLine(v); err != nil {
				return nil, err
			}
			sampling := map[string]string{"runs": "1"}
			switch v.Kind {
			case "test":
				if err := pythonApplicationTestArgs(v); err != nil {
					return nil, err
				}
			case "conformance":
				if len(v.Args) == 0 {
					return nil, fmt.Errorf("conformance requires a driver")
				}
				name := v.Args[0]
				if name == "" || path.IsAbs(name) || strings.Contains(name, "\\") {
					return nil, fmt.Errorf("invalid driver path")
				}
				// Resolve each component before cleaning, including canceled parents.
				current := filepath.Join(root, filepath.FromSlash(v.WorkingDir))
				for _, part := range strings.Split(name, "/") {
					current = filepath.Join(current, part)
					rel, e := filepath.Rel(root, current)
					if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						return nil, fmt.Errorf("driver escapes application")
					}
					info, e := os.Lstat(current)
					if e != nil {
						return nil, e
					}
					if info.Mode()&os.ModeSymlink != 0 {
						return nil, fmt.Errorf("symlink driver path")
					}
				}
				info, e := os.Stat(current)
				if e != nil || !info.Mode().IsRegular() || current != filepath.Join(root, driver) || current != filepath.Join(root, filepath.FromSlash(decls[v.Check].Source)) {
					return nil, fmt.Errorf("conformance driver does not match manifest and selected source")
				}
				sampling = map[string]string{"runs": "1000", "steps": "40", "seed": "1"}
				for k, value := range v.Sampling {
					_, known := sampling[k]
					n, e := strconv.ParseUint(value, 10, 31)
					if !known || !pythonApplicationDecimal.MatchString(value) || e != nil || (n == 0 && k != "seed") {
						return nil, fmt.Errorf("invalid sampling %s=%q", k, value)
					}
					sampling[k] = strconv.FormatUint(n, 10)
				}
			default:
				return nil, fmt.Errorf("unsupported Python application invocation kind %q", v.Kind)
			}
			out = append(out, clonePythonApplicationInvocation(PythonApplicationInvocation{PythonApplicationInvocationKey{check.Target, v.Check, v.Kind, v.ID}, v, sampling, v.Kind == "conformance" && p.Manifest.Exhaustive}))
		}
	}
	// A trusted contract's backing tests are required: each runs through one
	// of its declared invocations, keyed by the contract's declaration, which
	// no check can target since contract declarations lie outside scope.
	for _, c := range inv.TrustedContracts {
		for _, id := range c.Tests {
			if !slices.ContainsFunc(c.Invocations, func(v project.ResolvedComponentInvocation) bool { return v.Check == id }) {
				return nil, fmt.Errorf("trusted contract %q's backing test %s has no executable invocation", c.ID, id)
			}
		}
		for _, v := range c.Invocations {
			if err := pythonApplicationCommandLine(v); err != nil {
				return nil, err
			}
			if err := pythonApplicationTestArgs(v); err != nil {
				return nil, err
			}
			out = append(out, clonePythonApplicationInvocation(PythonApplicationInvocation{PythonApplicationInvocationKey{c.Contract, v.Check, v.Kind, v.ID}, v, map[string]string{"runs": "1"}, false}))
		}
	}
	if !slices.ContainsFunc(out, func(v PythonApplicationInvocation) bool { return v.Key.Kind == "conformance" }) {
		return nil, fmt.Errorf("Python application requires conformance")
	}
	return out, nil
}

// pythonApplicationCommandLine checks what every invocation shares: the
// literal python command, arguments without NUL, and an environment that
// leaves the gate's own settings alone.
func pythonApplicationCommandLine(v project.ResolvedComponentInvocation) error {
	if v.Command != "python" {
		return fmt.Errorf("invocation %s requires command python", v.ID)
	}
	for _, a := range v.Args {
		if strings.ContainsRune(a, 0) {
			return fmt.Errorf("NUL in invocation argument")
		}
	}
	for k, value := range v.Env {
		upper := strings.ToUpper(k)
		if !pythonApplicationEnv.MatchString(k) || strings.ContainsRune(value, 0) ||
			strings.HasPrefix(upper, "PYTHON") || strings.HasPrefix(upper, "INVARIANT_") ||
			strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") ||
			strings.HasSuffix(upper, "_PROXY") || upper == "DRIVER" || upper == "PATH" || upper == "HOME" {
			return fmt.Errorf("reserved or invalid environment key/value %q", k)
		}
	}
	return nil
}

func pythonApplicationTestArgs(v project.ResolvedComponentInvocation) error {
	if len(v.Sampling) != 0 || len(v.Args) < 3 || v.Args[0] != "-m" || v.Args[1] != "unittest" {
		return fmt.Errorf("test invocation requires explicit unittest selections and no sampling")
	}
	for _, name := range v.Args[2:] {
		if name == "discover" || !pythonApplicationName.MatchString(name) {
			return fmt.Errorf("invalid unittest selection %q", name)
		}
	}
	return nil
}

func pythonApplicationSnapshot(root string) (map[string]pythonApplicationFile, []project.ComponentFile, error) {
	state := map[string]pythonApplicationFile{}
	var files []project.ComponentFile
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular staged input %s", name)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		f := pythonApplicationFile{info: info}
		if !info.IsDir() {
			b, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			f.hash = pythonApplicationHash(b)
			files = append(files, project.ComponentFile{Path: rel, SHA256: f.hash})
		}
		state[rel] = f
		return nil
	})
	slices.SortFunc(files, func(a, b project.ComponentFile) int { return strings.Compare(a.Path, b.Path) })
	return state, files, err
}

func (s *PythonApplicationSession) fresh() error {
	state, _, err := pythonApplicationSnapshot(s.root)
	if err != nil {
		return err
	}
	if len(state) != len(s.snapshot) {
		return fmt.Errorf("Python application snapshot membership changed")
	}
	for name, before := range s.snapshot {
		after, ok := state[name]
		if !ok || !os.SameFile(before.info, after.info) || before.hash != after.hash || before.info.Mode() != after.info.Mode() || !before.info.ModTime().Equal(after.info.ModTime()) {
			return fmt.Errorf("Python application snapshot changed: %s", name)
		}
	}
	return nil
}

func selectPythonApplicationImage(ctx context.Context, w *PythonApplicationWorkspace) (string, *toolchain.PythonDependencies, error) {
	image, deps, err := w.SelectImage(ctx)
	if err != nil || deps != nil {
		return image, deps, err
	}
	// Workspace.SelectImage remains Docker-free without requirements. Session
	// preparation additionally acquires the pinned base; execution never pulls.
	b, err := toolchain.Docker(ctx, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image).Output()
	if err == nil && strings.TrimSpace(string(b)) == "linux/amd64" {
		return image, nil, nil
	}
	b, err = toolchain.Docker(ctx, "pull", "--platform", "linux/amd64", image).CombinedOutput()
	if err != nil {
		return "", nil, fmt.Errorf("acquiring pinned Python image: %w: %s", err, b)
	}
	return image, nil, nil
}

// An environment script's inline metadata replaces root requirements, as with
// `uv run`; its evidence must come from that script as staged (#320).
func pythonApplicationDependencies(files []project.ComponentFile, environment, image string, d *toolchain.PythonDependencies) error {
	hashes := map[string]string{}
	for _, f := range files {
		hashes[f.Path] = f.SHA256
	}
	if environment == "" && hashes["requirements.txt"] == "" {
		if image != toolchain.PythonImage || d != nil {
			return fmt.Errorf("standard-library session requires the pinned base and no dependency evidence")
		}
		return nil
	}
	if !pythonApplicationDigest.MatchString(image) || d == nil || d.Image != image || d.Base != toolchain.PythonImage || d.Platform != "linux/amd64" || !pythonApplicationDigest.MatchString(d.Recipe) || len(d.Inputs) == 0 || len(d.CanonicalInputs) != 2 || d.Artifacts == nil {
		return fmt.Errorf("incomplete Python dependency evidence")
	}
	if environment != "" {
		if d.Script == nil || d.Script.Path != environment || !pythonApplicationDigest.MatchString(d.Script.SHA256) || len(d.Inputs) != 1 || d.Inputs[0].Path != environment || hashes[environment] == "" || d.Inputs[0].SHA256 != hashes[environment] {
			return fmt.Errorf("dependency evidence does not come from %s's inline metadata as staged", environment)
		}
	} else {
		if d.Script != nil {
			return fmt.Errorf("dependency evidence names an environment script the manifest does not")
		}
		seen := map[string]bool{}
		for i, in := range d.Inputs {
			if seen[in.Path] || hashes[in.Path] != in.SHA256 || !pythonApplicationDigest.MatchString(in.SHA256) || (i > 0 && d.Inputs[i-1].Path >= in.Path) {
				return fmt.Errorf("invalid original dependency input %s", in.Path)
			}
			seen[in.Path] = true
		}
		if !seen["requirements.txt"] {
			return fmt.Errorf("dependency evidence omits root requirements")
		}
	}
	seen := map[string]bool{}
	for i, in := range d.CanonicalInputs {
		if seen[in.Path] || (in.Path != "requirements.txt" && in.Path != "constraints.txt") || !pythonApplicationDigest.MatchString(in.SHA256) || (i > 0 && d.CanonicalInputs[i-1].Path >= in.Path) {
			return fmt.Errorf("invalid canonical dependency input")
		}
		seen[in.Path] = true
	}
	seen = map[string]bool{}
	for i, a := range d.Artifacts {
		parts := strings.Split(strings.TrimSuffix(a.Filename, ".whl"), "-")
		if !pythonApplicationDistribution.MatchString(a.Name) || !pythonApplicationVersion.MatchString(a.Version) || path.Base(a.Filename) != a.Filename || !strings.HasSuffix(a.Filename, ".whl") || !pythonApplicationDigest.MatchString(a.SHA256) || seen[a.Name] || (i > 0 && d.Artifacts[i-1].Name >= a.Name) ||
			(len(parts) != 5 && len(parts) != 6) || pythonApplicationWheelName.ReplaceAllString(strings.ToLower(parts[0]), "-") != a.Name || parts[1] != a.Version {
			return fmt.Errorf("invalid wheel evidence")
		}
		seen[a.Name] = true
	}
	return nil
}
