package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A driver is evidence about the code only if it imports it, directly or
// through its own helpers, by a relative path or a tsconfig alias.
func TestTheDriverMustImportTheCode(t *testing.T) {
	src := t.TempDir()
	writeFiles(t, src, map[string]string{
		"tsconfig.json":                           `{"compilerOptions": {"paths": {"@/*": ["./src/*"]}}}`,
		"src/lib/store.ts":                        "export class Store {}",
		"src/other/clock.ts":                      "export const now = () => 0",
		"migrations/admin/001.sql":                "create table t (x int);",
		"invariant/leases/conformance.ts":         "import { writeFileSync } from \"node:fs\";\nimport { run } from \"./harness\";\n",
		"invariant/leases/harness.ts":             "import { Store } from '../../src/lib/store';\nexport const run = () => new Store();\n",
		"invariant/aliased/conformance.ts":        "import { now } from \"@/other/clock\";\n",
		"invariant/nothing/conformance.ts":        "const fake = [{ status: \"queued\" }];\n",
		"invariant/leases/.invariant/specs/L.tla": "---- MODULE L ----\n====\n",
	})
	existing := []string{"src/lib", "src/other", "migrations/admin"}
	if got := strings.Join(codeEntries(src, existing), ","); got != "src/lib,src/other" {
		t.Errorf("code entries %s; the migrations hold no code", got)
	}
	reached := func(driver string) string {
		paths, err := importedPaths(src, driver)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(paths, ",")
	}
	if got := reached("invariant/leases/conformance.ts"); !strings.Contains(got, "src/lib/store") {
		t.Errorf("through its harness, the driver reaches %s", got)
	}
	if got := reached("invariant/aliased/conformance.ts"); got != "src/other/clock" {
		t.Errorf("through @/, the driver reaches %s", got)
	}
	if got := reached("invariant/nothing/conformance.ts"); got != "" {
		t.Errorf("a driver that imports nothing reaches %s", got)
	}
	if _, err := importedPaths(src, "invariant/missing/conformance.ts"); err == nil {
		t.Error("a missing driver should be an error")
	}
}

func TestPackageRootAndStaging(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"package.json":                       `{"name": "app"}`,
		"package-lock.json":                  `{"lockfileVersion": 3}`,
		"src/lib/store.ts":                   "export class Store {}",
		"src/app/page.tsx":                   "secret app code the driver doesn't need",
		"migrations/admin/001.sql":           "create table t (x int);",
		"invariant/leases/.invariant/x.json": "{}",
	})
	got, rel, err := PackageRoot(filepath.Join(root, "invariant", "leases"))
	if err != nil || rel != "invariant/leases" {
		t.Fatalf("root %s, rel %s, err %v", got, rel, err)
	}
	if want, _ := filepath.EvalSymlinks(root); func() string { g, _ := filepath.EvalSymlinks(got); return g }() != want {
		t.Errorf("root %s, want %s", got, root)
	}
	dst := t.TempDir()
	if err := StagePackage(root, []string{"src/lib", "migrations/admin"}, dst); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"package.json", "package-lock.json", "src/lib/store.ts", "migrations/admin/001.sql"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("%s wasn't staged", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "src/app/page.tsx")); err == nil {
		t.Error("staging copied code the project doesn't name")
	}
	if err := StagePackage(root, []string{"src/gone"}, t.TempDir()); err == nil {
		t.Error("staging code that isn't there should fail")
	}
	a, _ := HashExisting(root, []string{"src/lib"})
	os.WriteFile(filepath.Join(root, "src/lib/store.ts"), []byte("export class Store { changed = true }"), 0o644)
	b, _ := HashExisting(root, []string{"src/lib"})
	if a[0].Files != 1 || a[0].SHA256 == b[0].SHA256 {
		t.Errorf("hashes %v and %v: a change to the code must change its hash", a, b)
	}
	if _, _, err := PackageRoot(t.TempDir()); err == nil {
		t.Error("a project with no package above it should fail")
	}
}
