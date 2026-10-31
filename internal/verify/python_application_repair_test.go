package verify

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPythonApplicationExecutionOutputFiles(t *testing.T) {
	if which := os.Getenv("PA315_OUTPUT_CASE"); which != "" {
		pa315OutputFile(t, which)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, which := range []string{"regular", "limit", "oversized", "missing", "directory", "symlink", "device symlink", "pipe"} {
		t.Run(which, func(t *testing.T) {
			// A regression must fail promptly even if it follows /dev/zero or
			// blocks opening the application's pipe.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPythonApplicationExecutionOutputFiles$", "-test.count=1")
			cmd.Env = append(os.Environ(), "PA315_OUTPUT_CASE="+which)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("output file %s: %v (%v)\n%s", which, err, ctx.Err(), b)
			}
		})
	}
}

func pa315OutputFile(t *testing.T, which string) {
	t.Helper()
	bin := t.TempDir()
	pa310Write(t, bin, "docker", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, "docker"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output := t.TempDir()
	name := filepath.Join(output, "events.jsonl")
	want := []byte("dedicated event bytes\n")
	var err error
	switch which {
	case "regular":
		err = os.WriteFile(name, want, 0600)
	case "limit", "oversized":
		f, e := os.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		size := int64(pythonApplicationEventsLimit)
		if which == "oversized" {
			size++
		}
		err = f.Truncate(size)
		if e := f.Close(); e != nil {
			t.Fatal(e)
		}
	case "missing":
	case "directory":
		err = os.Mkdir(name, 0700)
	case "symlink":
		host := filepath.Join(t.TempDir(), "host.txt")
		if err := os.WriteFile(host, []byte("host bytes must not be read"), 0600); err != nil {
			t.Fatal(err)
		}
		err = os.Symlink(host, name)
	case "device symlink":
		err = os.Symlink("/dev/zero", name)
	case "pipe":
		err = syscall.Mkfifo(name, 0600)
	default:
		t.Fatalf("unknown case %q", which)
	}
	if err != nil {
		t.Fatal(err)
	}
	p, err := RunPythonApplicationCommand(context.Background(), PythonApplicationCommand{
		Root: t.TempDir(), RuntimeDir: t.TempDir(), OutputDir: output,
		Image: pa315Image, Interpreter: "/usr/local/bin/python", WorkingDir: "/src",
	})
	if p.ExitCode != 0 {
		t.Fatalf("executor double failed: %+v %v", p, err)
	}
	switch which {
	case "regular":
		if err != nil || !bytes.Equal(p.Events, want) {
			t.Fatalf("regular events changed: %q %v", p.Events, err)
		}
	case "limit":
		if err != nil || len(p.Events) != pythonApplicationEventsLimit {
			t.Fatalf("bounded regular output refused: %d bytes, %v", len(p.Events), err)
		}
	case "oversized":
		if err == nil || !strings.Contains(err.Error(), "exceed") || len(p.Events) != pythonApplicationEventsLimit {
			t.Fatalf("size limit did not retain a bounded prefix: %d bytes, %v", len(p.Events), err)
		}
	default:
		if err == nil || len(p.Events) != 0 {
			t.Fatalf("unsafe output read: %d bytes, %v", len(p.Events), err)
		}
	}
}

func TestPythonApplicationExecutionSnapshotPathOrder(t *testing.T) {
	root, p := pa315Fixture(t, false)
	pa310Write(t, root, "tasks.txt", "sibling of the tasks directory\n")
	before := pa315Tree(t, root)
	opts := pa315Options(new(int))
	opts.Execute = func(_ context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
		return PythonApplicationProcess{Events: pa315Stream(t, c,
			pa315Event(c, before[pa315TestSource], "passed"), map[string]any{"type": "end", "count": 1})}, nil
	}
	s := pa315Prepare(t, p, opts)
	prov := s.Provenance()
	var names []string
	for _, f := range prov.Files {
		names = append(names, f.Path)
	}
	if !slices.IsSorted(names) || slices.Index(names, "tasks.txt") >= slices.Index(names, pa315Target) {
		t.Fatalf("snapshot is not sorted by full path: %v", names)
	}
	other := pa315Prepare(t, p, pa315Options(new(int)))
	if !reflect.DeepEqual(prov, other.Provenance()) {
		t.Fatal("relocation changed snapshot provenance")
	}
	var fingerprint string
	for i := 0; i < 2; i++ {
		r, err := s.Run(context.Background(), pa315Unit(t, s).Key)
		if err != nil || r == nil || !r.Success {
			t.Fatalf("unchanged snapshot failed: %+v %v", r, err)
		}
		if i > 0 && r.Fingerprint != fingerprint {
			t.Fatal("unchanged snapshot changed result fingerprint")
		}
		fingerprint = r.Fingerprint
	}
	if !reflect.DeepEqual(before, pa315Tree(t, root)) {
		t.Fatal("original bytes changed")
	}
}
