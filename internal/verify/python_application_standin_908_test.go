package verify

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestPythonApplicationStandInIncompleteMetadata908(t *testing.T) {
	files := standIn908Files(t)
	hashes := map[string]string{}
	for name, b := range files {
		hashes["nagini_contracts/"+name] = pythonApplicationHash(b)
	}
	revision := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, vcs, revision, modified, want string
	}{
		{"absent", "", "", "", "unknown"},
		{"missing vcs", "", revision, "false", "incomplete"},
		{"missing revision", "git", "", "false", "incomplete"},
		{"missing cleanliness", "git", revision, "", "incomplete"},
		{"abbreviated revision", "git", revision[:12], "false", "incomplete"},
		{"nonhex revision", "git", strings.Repeat("z", 40), "false", "incomplete"},
		{"revision newline", "git", revision + "\n", "false", "incomplete"},
		{"other vcs", "hg", revision, "false", "incomplete"},
		{"invalid cleanliness", "git", revision, "0", "incomplete"},
		{"modified without revision", "git", "", "true", "modified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var info *debug.BuildInfo
			if tc.name != "absent" {
				info = &debug.BuildInfo{Settings: []debug.BuildSetting{
					{Key: "vcs", Value: tc.vcs},
					{Key: "vcs.revision", Value: tc.revision},
					{Key: "vcs.modified", Value: tc.modified},
				}}
			}
			root := filepath.Join(t.TempDir(), "original application")
			got := pythonApplicationStandInInstructions(root, hashes, info)
			standIn908Diagnostic(t, got, root, files)
			for _, want := range []string{"Build identity: " + tc.want, "Build and run", "before obtaining copy instructions", "has not inspected CI"} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q: %s", want, got)
				}
			}
			if strings.Contains(got, "github.com/gitdek/invariant/tree/") || strings.Contains(got, "```sh") {
				t.Fatalf("unreproducible build supplied copy instructions: %s", got)
			}
		})
	}
}

func TestPythonApplicationStandInCopyRefusals908(t *testing.T) {
	files := standIn908Files(t)
	checkout := filepath.Join(t.TempDir(), "checkout with spaces")
	hashes := map[string]string{}
	for name, b := range files {
		pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/"+name, string(b))
		hashes["nagini_contracts/"+name] = pythonApplicationHash(b)
	}
	standIn908Git(t, checkout, "init", "-q")
	standIn908Git(t, checkout, "add", ".")
	standIn908Git(t, checkout, "commit", "-qm", "stand-in recipe fixture")
	revision := strings.TrimSpace(standIn908Git(t, checkout, "rev-parse", "HEAD"))
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: "false"},
	}}
	for _, name := range []string{"changed initializer", "missing initializer", "existing file", "existing directory", "symlink", "dangling symlink", "unset checkout", "unset application", "empty checkout", "empty application"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "application with spaces")
			pa342Write(t, root, "owned.py", "# application-owned bytes\n")
			destination := filepath.Join(root, "nagini_contracts")
			initializer := filepath.Join(checkout, "internal/verify/naginiruntime/nagini_contracts/__init__.py")
			pa342Write(t, checkout, "internal/verify/naginiruntime/nagini_contracts/__init__.py", string(files["__init__.py"]))
			var err error
			switch name {
			case "changed initializer":
				err = os.WriteFile(initializer, []byte("# changed\n"), 0o644)
			case "missing initializer":
				err = os.Remove(initializer)
			case "existing file":
				err = os.WriteFile(destination, []byte("# owned\n"), 0o644)
			case "existing directory":
				err = os.Mkdir(destination, 0o755)
			case "symlink":
				pa342Write(t, base, "outside/owned.py", "# outside the application\n")
				err = os.Symlink(filepath.Join(base, "outside"), destination)
			case "dangling symlink":
				err = os.Symlink(filepath.Join(base, "absent"), destination)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := standIn908CopyTree(t, base)
			recipe := standIn908Recipe(t, pythonApplicationStandInInstructions(root, hashes, info))
			if recipe == "" {
				t.Fatal("no recipe")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "sh", "-c", recipe)
			cmd.Dir = root
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "INVARIANT_CHECKOUT=") && !strings.HasPrefix(entry, "APPLICATION_ROOT=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			for _, v := range []struct{ key, value, label string }{
				{"INVARIANT_CHECKOUT", checkout, "checkout"}, {"APPLICATION_ROOT", root, "application"},
			} {
				if name == "unset "+v.label {
					continue
				}
				if name == "empty "+v.label {
					v.value = ""
				}
				cmd.Env = append(cmd.Env, v.key+"="+v.value)
			}
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("recipe did not refuse %s: %s", name, out)
			}
			if after := standIn908CopyTree(t, base); !reflect.DeepEqual(before, after) {
				t.Fatalf("refused copy changed application or symlink target: before=%v, after=%v", before, after)
			}
		})
	}
}

// Include empty directories and dangling links when checking refused copies.
func standIn908CopyTree(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			state[name] = "directory"
		case entry.Type()&os.ModeSymlink != 0:
			state[name], err = os.Readlink(name)
		default:
			var b []byte
			b, err = os.ReadFile(name)
			state[name] = pythonApplicationHash(b)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
