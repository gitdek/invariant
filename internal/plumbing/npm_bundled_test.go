package plumbing

import (
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/repository"
)

// A locked dependency may bundle packages, as Tailwind's optional wasm32 build
// does: they ship inside its own integrity-checked tarball, and npm records no
// entries for them when the platform skips it. Packages recorded as bundled
// inside another, and bundling at the root, are still refused.
func TestNpmLockAdmitsADependencysBundledPackages(t *testing.T) {
	const integrity = `"sha512-` + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==" + `"`
	part := repository.Part{Name: "web", Directory: ".", Language: "typescript", Image: "node@sha256:" + strings.Repeat("a", 64),
		Inputs: []string{"package.json"}, Lockfiles: []string{"package-lock.json"},
		Commands: map[string]repository.Command{"install": {Argv: []string{"npm", "ci"}}}}
	manifest := `{"name":"app","version":"1.0.0","devDependencies":{"tslib":"2.6.0","wasm":"4.3.3"}}`
	lock := func(root, dependency string) string {
		return `{"name":"app","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{` +
			`"":{"name":"app","version":"1.0.0","devDependencies":{"tslib":"2.6.0","wasm":"4.3.3"}` + root + `},` +
			`"node_modules/tslib":{"version":"2.6.0","resolved":"https://registry.npmjs.org/tslib/-/tslib-2.6.0.tgz","integrity":` + integrity + `,"dev":true},` +
			`"node_modules/wasm":{"version":"4.3.3","resolved":"https://registry.npmjs.org/wasm/-/wasm-4.3.3.tgz","integrity":` + integrity +
			`,"dev":true,"optional":true,"cpu":["wasm32"]` + dependency + `}}}`
	}
	for _, c := range []struct {
		name, root, dependency string
		refused                bool
	}{
		{name: "plain dependency"},
		{name: "dependency bundles packages", dependency: `,"bundleDependencies":["tslib"]`},
		{name: "dependency bundles packages, older spelling", dependency: `,"bundledDependencies":["tslib"]`},
		// The bundled tslib comes from wasm's own tarball, so the tree's
		// older tslib needn't satisfy it, as npm records none on this platform.
		{name: "bundled dependency newer than the tree's", dependency: `,"dependencies":{"tslib":"^2.8.0"},"bundleDependencies":["tslib"]`},
		{name: "all dependencies bundled", dependency: `,"dependencies":{"tslib":"^2.8.0"},"bundleDependencies":true`},
		{name: "unbundled dependency newer than the tree's", dependency: `,"dependencies":{"tslib":"^2.8.0"},"bundleDependencies":["other"]`, refused: true},
		{name: "malformed bundle", dependency: `,"bundleDependencies":"tslib"`, refused: true},
		{name: "root bundles packages", root: `,"bundleDependencies":["wasm"]`, refused: true},
		{name: "package recorded inside a bundle", dependency: `,"inBundle":true`, refused: true},
		{name: "linked package", dependency: `,"link":true`, refused: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := CheckDependencyInstall(part, map[string][]byte{"package.json": []byte(manifest), "package-lock.json": []byte(lock(c.root, c.dependency))})
			if c.refused != (err != nil) {
				t.Fatalf("refused = %v, want %v: %v", err != nil, c.refused, err)
			}
		})
	}
}
