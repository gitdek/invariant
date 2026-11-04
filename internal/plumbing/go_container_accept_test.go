package plumbing

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Only Docker transport is replaced. These bytes exercise file transport, not
// package discovery: all discovery-success tests use real Go processes.
func sourceDocker527(t *testing.T, source string) (string, string) {
	t.Helper()
	bin, logs := docker484(t)
	archiveFile := filepath.Join(t.TempDir(), "source.tar")
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: "ready_accept_test.go", Mode: 0o644, Size: int64(len(source)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(source)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archiveFile, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := `#!/bin/sh
set -eu
printf '%s\000' "$@" > LOGDIR/$$
case "$1" in
 create) echo fixture-container ;;
 cp)
   case "$2" in
     invariant-deps-*:*) cat ARCHIVE ;;
     *) cat > /dev/null ;;
   esac ;;
 exec) echo 'container diagnostic' ;;
 wait) echo 0 ;;
esac
`
	script = strings.ReplaceAll(strings.ReplaceAll(script, "LOGDIR", quote(logs)), "ARCHIVE", quote(archiveFile))
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return logs, archiveFile
}

func TestGoAcceptancePreparedContainerBoundary(t *testing.T) {
	source := "package fixture\r\nimport \"testing\"\r\nfunc TestReady(t *testing.T) {}\r\n"
	logs, archiveFile := sourceDocker527(t, source)
	q := request484(t, "go")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	environment, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer close484(t, environment)
	reader, ok := environment.(RepositoryAcceptanceFileEnvironment)
	if !ok {
		t.Fatal("prepared Go environment cannot read its own included source files")
	}
	data, err := reader.ReadFile(ctx, "ready_accept_test.go")
	if err != nil || string(data) != source {
		t.Fatalf("prepared file bytes changed: %v %q", err, data)
	}
	for _, name := range []string{"../ready_accept_test.go", "/etc/passwd", "a/../ready_accept_test.go", "a\\ready_accept_test.go", ".git/config", "ready\x00_test.go"} {
		if _, err := reader.ReadFile(ctx, name); err == nil {
			t.Errorf("unsafe source read accepted: %q", name)
		}
	}
	// A container archive must never turn a symbolic/hard link into a regular
	// pinned source. No target contents are read or accepted from these records.
	for _, kind := range []byte{tar.TypeSymlink, tar.TypeLink} {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		if err := w.WriteHeader(&tar.Header{Name: "ready_accept_test.go", Typeflag: kind, Linkname: "/outside"}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(archiveFile, b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ReadFile(ctx, "ready_accept_test.go"); err == nil {
			t.Fatal("linked source became discovery input")
		}
	}
	part := goPart527("go", "test", "./...", "-run", "^{test}$", "-count=1")
	part.Directory = q.Directory
	part.Name = "app"
	test := goTest527("app/nested/ready_accept_test.go", "TestReady/$(touch_PWN);`echo_bad`/*glob*/--run=Other")
	test.Part = "app"
	selection, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := environment.Run(ctx, selection.Argv); err != nil {
		t.Fatal(err)
	}
	var forwarded bool
	for _, call := range invocations484(t, logs) {
		if len(call) == 0 {
			continue
		}
		if call[0] == "pull" || call[0] == "build" || call[0] == "commit" {
			t.Fatalf("discovery acquired/published tools: %q", call)
		}
		for _, arg := range call {
			if strings.Contains(arg, "python") || arg == "--mount" || arg == "-v" || arg == "--volume" {
				t.Fatalf("host cache or Python added to prepared environment: %q", call)
			}
		}
		if call[0] == "create" {
			if flag484(call, "--network") != "none" || flag484(call, "--pull") != "never" || flag484(call, "--workdir", "-w") != "/invariant/work/app" || !slices.Contains(call, "--init") {
				t.Fatalf("prepared isolation changed: %q", call)
			}
		}
		if call[0] != "exec" {
			continue
		}
		i := slices.Index(call, "go")
		if i < 0 {
			continue
		}
		forwarded = true
		if !slices.Equal(call[i:], selection.Argv) || !slices.Contains(call[:i], "GOPROXY=off") || !slices.Contains(call[:i], "GOTOOLCHAIN=local") || !slices.Contains(call[:i], "GOCACHE=/tmp/gocache") || !slices.Contains(call[:i], "GOWORK=off") {
			t.Fatalf("lost literal arguments or offline/cache policy: %q", call)
		}
		if flag484(call[:i], "--workdir", "-w") != "/invariant/work/app" {
			t.Fatal("Go command ran outside its owning part")
		}
		for _, arg := range call[:i] {
			if strings.Contains(arg, "touch_PWN") || strings.Contains(arg, "*glob*") {
				t.Fatal("selector was interpolated into wrapper source")
			}
		}
	}
	if !forwarded {
		t.Fatal("production container boundary never forwarded selected Go arguments")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := reader.ReadFile(canceled, "ready_accept_test.go"); err == nil {
		t.Fatal("canceled prepared read succeeded")
	}
}
