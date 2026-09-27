package toolchain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// NodeDepsBase is where a dependencies image starts (D-0054):
// node:24-bookworm-slim, multi-arch index digest. It's Debian rather than
// Alpine because native modules, such as better-sqlite3, ship prebuilt for
// glibc.
const NodeDepsBase = "node@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6"

// depsRecipe installs a package's locked dependencies, and nothing else.
const depsRecipe = "FROM " + NodeDepsBase + `
WORKDIR /deps
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund --loglevel=error
`

// DepsRecipe names a dependencies image by what goes into it: the recipe,
// and the package's manifest and lockfile. Receipts record it rather than
// the image, because npm builds native modules for the machine it runs on,
// and a local run and a CI run must agree.
func DepsRecipe(packageJSON, lock []byte) string {
	h := sha256.New()
	for _, b := range [][]byte{[]byte(depsRecipe), packageJSON, lock} {
		fmt.Fprintf(h, "%d\n", len(b))
		h.Write(b)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// DepsImage builds, once, the image holding the locked dependencies of the
// package in root, and returns its tag and its recipe. Only the build has
// network access, to fetch what the lockfile pins by integrity hash. Nothing
// that later runs in the image has any.
func DepsImage(ctx context.Context, root string) (tag, recipe string, err error) {
	pkg, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", "", err
	}
	lock, err := os.ReadFile(filepath.Join(root, "package-lock.json"))
	if err != nil {
		return "", "", fmt.Errorf("existing code is run with its locked dependencies, and there's no package-lock.json: %w", err)
	}
	recipe = DepsRecipe(pkg, lock)
	tag = "invariant-deps:" + recipe[len("sha256:"):][:16]
	if exec.CommandContext(ctx, "docker", "image", "inspect", tag).Run() == nil {
		return tag, recipe, nil
	}
	dir, err := os.MkdirTemp("", "invariant-deps-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(dir)
	for name, b := range map[string][]byte{"Dockerfile": []byte(depsRecipe), "package.json": pkg, "package-lock.json": lock} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return "", "", err
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "build", "-q", "-t", tag, dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("installing the package's locked dependencies: %w\n%s", err, out.String())
	}
	return tag, recipe, nil
}
