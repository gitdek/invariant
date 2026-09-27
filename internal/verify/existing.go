package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/toolchain"
)

// ExistingTypeScript checks TypeScript code that was already in its
// repository (D-0054). The project holds only the model and a conformance
// driver. The driver runs the real code, on a throwaway copy of the package
// it belongs to, in an image with the package's locked dependencies, with no
// network and none of the host's environment.
type ExistingTypeScript struct{}

func (ExistingTypeScript) Verify(context.Context, string) (*Code, error) { return nil, nil }

func (ExistingTypeScript) Check(ctx context.Context, p *project.Project) (Build, Evidence, error) {
	root, rel, err := PackageRoot(p.Dir)
	if err != nil {
		return Build{}, Evidence{}, err
	}
	image, _, err := toolchain.DepsImage(ctx, root)
	if err != nil {
		return Build{}, Evidence{}, err
	}
	work, err := os.MkdirTemp("", "invariant-existing-")
	if err != nil {
		return Build{}, Evidence{}, err
	}
	defer os.RemoveAll(work)
	src, out := filepath.Join(work, "src"), filepath.Join(work, "out")
	if err := StagePackage(root, p.Manifest.Existing, src); err != nil {
		return Build{}, Evidence{}, err
	}
	if err := copyTree(p.Dir, filepath.Join(src, filepath.FromSlash(rel))); err != nil {
		return Build{}, Evidence{}, err
	}
	if err := os.MkdirAll(out, 0o777); err != nil {
		return Build{}, Evidence{}, err
	}
	driver := path.Join(rel, p.Manifest.Conformance)

	// The driver's runs are evidence about the code only if it runs the
	// code, so it must import every piece of code the project names.
	b := Build{Passed: true}
	reached, err := importedPaths(src, driver)
	if err != nil {
		return Build{}, Evidence{}, err
	}
	var missing []string
	for _, e := range codeEntries(src, p.Manifest.Existing) {
		ok := false
		for _, r := range reached {
			if r == e || strings.HasPrefix(r, e+"/") {
				ok = true
			}
		}
		b.Steps = append(b.Steps, BuildStep{Name: "the driver imports " + e, Passed: ok})
		if !ok {
			b.Passed = false
			missing = append(missing, e)
		}
	}
	if !b.Passed {
		b.Output = "The conformance driver doesn't import " + strings.Join(missing, ", ") + ", so its runs say nothing about that code."
		return b, Evidence{Message: "the driver doesn't run all the code it checks"}, nil
	}

	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "--memory", "2g", "--pids-limit", "512",
		"-e", "HOME=/tmp", "-e", "DRIVER="+driver, "-e", "INVARIANT_TRACES=/out/traces.json",
		"-e", fmt.Sprintf("INVARIANT_RUNS=%d", conformanceRuns), "-e", fmt.Sprintf("INVARIANT_STEPS=%d", conformanceSteps),
		"-e", fmt.Sprintf("INVARIANT_SEED=%d", conformanceSeed),
		"-v", src+":/src", "-v", out+":/out", "-w", "/src", image, "sh", "-c",
		// tsx runs TypeScript that Node can't, when the package has it.
		`ln -s /deps/node_modules node_modules && if [ -x node_modules/.bin/tsx ]; then node_modules/.bin/tsx "$DRIVER"; else node "$DRIVER"; fi ; echo "@@invariant conform=$?"`)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return b, Evidence{}, fmt.Errorf("running the sandbox: %w", err)
		}
	}
	sections, err := splitMarkers(buf.String(), 1)
	if err != nil {
		return b, Evidence{}, err
	}
	traces, readErr := os.ReadFile(filepath.Join(out, "traces.json"))
	if sections[0].code != 0 || readErr != nil {
		return b, Evidence{Message: "the conformance driver failed:\n" + lastLines(sections[0].out, 30)}, nil
	}
	return b, Evidence{Traces: traces}, nil
}

// PackageRoot finds the package a project's existing code belongs to: the
// nearest directory above the project with a package-lock.json. It returns
// the package's root and the project's path inside it.
func PackageRoot(projectDir string) (root, rel string, err error) {
	dir, err := filepath.Abs(projectDir)
	if err != nil {
		return "", "", err
	}
	for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "package-lock.json")); err == nil {
			r, err := filepath.Rel(d, dir)
			return d, filepath.ToSlash(r), err
		}
		if filepath.Dir(d) == d {
			return "", "", fmt.Errorf("%s checks existing code, and no directory above it has a package-lock.json", projectDir)
		}
	}
}

// StagePackage copies what a driver needs from a package into dst: its
// package.json, lockfile and tsconfig.json, and the existing code it names.
// Nothing else of the package, such as its node_modules, comes along.
func StagePackage(root string, existing []string, dst string) error {
	for _, f := range []string{"package.json", "package-lock.json", "tsconfig.json"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if errors.Is(err, fs.ErrNotExist) && f == "tsconfig.json" {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeStaged(filepath.Join(dst, f), b); err != nil {
			return err
		}
	}
	for _, e := range existing {
		from := filepath.Join(root, filepath.FromSlash(e))
		info, err := os.Stat(from)
		if err != nil {
			return fmt.Errorf("the existing code %s isn't in the package: %w", e, err)
		}
		if !info.IsDir() {
			b, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			if err := writeStaged(filepath.Join(dst, filepath.FromSlash(e)), b); err != nil {
				return err
			}
			continue
		}
		if err := copyTree(from, filepath.Join(dst, filepath.FromSlash(e))); err != nil {
			return err
		}
	}
	return nil
}

func writeStaged(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// ExistingCode is one piece of existing code a project checked, as it was.
type ExistingCode struct {
	Path   string `json:"path"`
	Files  int    `json:"files"`
	SHA256 string `json:"sha256"` // over every file's path and contents, in order
}

// HashExisting records the existing code a project checks, so a receipt
// says exactly what was run.
func HashExisting(root string, existing []string) ([]ExistingCode, error) {
	var out []ExistingCode
	for _, e := range existing {
		from := filepath.Join(root, filepath.FromSlash(e))
		var files []string
		err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("the existing code %s: %w", e, err)
		}
		sort.Strings(files)
		h := sha256.New()
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			r, _ := filepath.Rel(root, f)
			fmt.Fprintf(h, "%s\n%d\n", filepath.ToSlash(r), len(b))
			h.Write(b)
		}
		out = append(out, ExistingCode{Path: e, Files: len(files), SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil))})
	}
	return out, nil
}

var sourceExts = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"}

func isSource(name string) bool {
	for _, e := range sourceExts {
		if strings.HasSuffix(name, e) && !strings.HasSuffix(name, ".d.ts") {
			return true
		}
	}
	return false
}

// codeEntries keeps the existing entries that hold source code. A directory
// of SQL migrations is data the code reads, not code to import.
func codeEntries(src string, existing []string) []string {
	var out []string
	for _, e := range existing {
		has := false
		filepath.WalkDir(filepath.Join(src, filepath.FromSlash(e)), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && isSource(d.Name()) {
				has = true
				return filepath.SkipAll
			}
			return nil
		})
		if has {
			out = append(out, e)
		}
	}
	return out
}

var importRE = regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*\(\s*|\bimport\s+|\brequire\s*\(\s*)["']([^"'\n]+)["']`)

// importedPaths follows the driver's imports through the project's own files
// and returns every package path they reach, resolving relative imports and
// tsconfig path aliases such as "@/*".
func importedPaths(src, driver string) ([]string, error) {
	aliases := tsconfigAliases(src)
	project := path.Dir(driver)
	seen := map[string]bool{}
	var reached []string
	queue := []string{driver}
	for len(queue) > 0 {
		file := queue[0]
		queue = queue[1:]
		if seen[file] {
			continue
		}
		seen[file] = true
		b, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(file)))
		if err != nil {
			if file == driver {
				return nil, fmt.Errorf("the conformance driver %s: %w", driver, err)
			}
			continue
		}
		for _, m := range importRE.FindAllStringSubmatch(string(b), -1) {
			spec := m[1]
			var target string
			switch {
			case strings.HasPrefix(spec, "./"), strings.HasPrefix(spec, "../"):
				target = path.Clean(path.Join(path.Dir(file), spec))
			default:
				for prefix, to := range aliases {
					if strings.HasPrefix(spec, prefix) {
						target = path.Clean(to + strings.TrimPrefix(spec, prefix))
					}
				}
			}
			if target == "" || strings.HasPrefix(target, "../") {
				continue
			}
			reached = append(reached, target)
			if target == project || strings.HasPrefix(target, project+"/") {
				for _, f := range candidates(src, target) {
					queue = append(queue, f)
				}
			}
		}
	}
	return reached, nil
}

// candidates are the files an extensionless import can name.
func candidates(src, target string) []string {
	var out []string
	for _, c := range []string{target, target + ".ts", target + ".tsx", target + ".mts", target + ".js", target + "/index.ts", target + "/index.js"} {
		if info, err := os.Stat(filepath.Join(src, filepath.FromSlash(c))); err == nil && !info.IsDir() {
			out = append(out, c)
		}
	}
	return out
}

// tsconfigAliases reads the path aliases that end in "/*", such as
// "@/*": ["./src/*"], as prefixes: "@/" to "src/".
func tsconfigAliases(src string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(src, "tsconfig.json"))
	if err != nil {
		return out
	}
	var cfg struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return out
	}
	for alias, targets := range cfg.CompilerOptions.Paths {
		if !strings.HasSuffix(alias, "/*") || len(targets) == 0 || !strings.HasSuffix(targets[0], "/*") {
			continue
		}
		to := path.Clean(path.Join(cfg.CompilerOptions.BaseURL, strings.TrimSuffix(targets[0], "*")))
		out[strings.TrimSuffix(alias, "*")] = strings.TrimPrefix(to, "./") + "/"
	}
	return out
}
