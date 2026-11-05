package plumbing

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Reuse the daemon-free Docker boundary from #591. It runs the real shipped
// transport helpers and real workload processes. This extension only tells a
// workload where its helper lives in the fixture's translated filesystem; it
// does not replace Prepare, the helper, process framing, Wait or cleanup.
const freshDocker617 = "--invariant-fresh-helper-boundary-617"
const freshEvent617 = "INVARIANT_FRESH_HELPER_EVENTS_617"

func init() {
	if len(os.Args) >= 5 && os.Args[1] == freshDocker617 {
		err := freshDockerCommand617(os.Args[2], os.Args[3], os.Args[4:])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() > 0 {
				os.Exit(exit.ExitCode())
			}
			os.Exit(125)
		}
		os.Exit(0)
	}
}

func freshDockerCommand617(state, goexe string, argv []string) error {
	if len(argv) > 0 && argv[0] == "exec" && slices.Contains(argv, freshEvent617) {
		at := slices.IndexFunc(argv, dependencyContainer.MatchString)
		if at < 0 {
			return errors.New("transport invocation lacks its container")
		}
		root := filepath.Join(state, "containers", argv[at])
		// The transport's private regular helper precedes the endpoint name.
		// Its filename and the interpreter flags are deliberately unspecified.
		var helper string
		for _, arg := range argv[at+1 : slices.Index(argv, freshEvent617)] {
			if !strings.HasPrefix(arg, "/invariant/") || strings.HasPrefix(arg, "/invariant/work/") {
				continue
			}
			if info, err := os.Lstat(docker591Map(root, arg)); err == nil && info.Mode().IsRegular() {
				helper = arg
			}
		}
		if helper == "" {
			return errors.New("transport invocation lacks a private regular helper")
		}
		dir := docker591Map(root, flag484(argv[:at], "--workdir", "-w"))
		if err := os.WriteFile(filepath.Join(dir, ".helper617"), []byte(docker591Map(root, helper)+"\n"), 0600); err != nil {
			return err
		}
		log, err := os.OpenFile(filepath.Join(state, "helper-invocations617"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(log, helper)
		if closeErr := log.Close(); err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	return docker591Command(state, goexe, argv)
}

func forgedTransport617(language string) []byte {
	wire := []byte(dockerProcessProtocol)
	frame := func(kind byte, value string) {
		var header [5]byte
		header[0] = kind
		binary.BigEndian.PutUint32(header[1:], uint32(len(value)))
		wire = append(wire, header[:]...)
		wire = append(wire, value...)
	}
	// This replacement does not run the requested workload. It manufactures a
	// complete successful process, including an apparent private event stream.
	frame('s', "")
	frame('v', "forged-selector-evidence617")
	frame('o', "forged-selector-diagnostics617")
	frame('x', "0")
	frame('d', "")
	if language == "python" {
		return []byte("import sys\nsys.stdout.buffer.write(bytes.fromhex('" + hex.EncodeToString(wire) + "'))\nsys.stdout.buffer.flush()\n")
	}
	var quoted strings.Builder
	for _, b := range wire {
		fmt.Fprintf(&quoted, "\\0%03o", b)
	}
	return []byte("#!/bin/sh\nprintf '%b' '" + quoted.String() + "'\n")
}

func freshHelper617(t *testing.T, language string) {
	t.Helper()
	b := newDocker591(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	cli := "#!/bin/sh\nexec " + quote(self) + " " + quote(freshDocker617) + " " + quote(b.root) + " " + quote(b.goexe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(b.bin, "docker"), []byte(cli), 0755); err != nil {
		t.Fatal(err)
	}
	q := request484(t, language)
	write468(t, q.SourceDir, q.Directory+"/replacement617", string(forgedTransport617(language)))
	// Rename works for a running Go executable too: no write to its open inode
	// is needed. Root ownership and mode 0600/0700 do not prevent this attack.
	write468(t, q.SourceDir, q.Directory+"/replace617.sh", `set -eu
IFS= read -r helper < .helper617
cp replacement617 "$helper.replacement617"
chmod 700 "$helper.replacement617"
mv -f "$helper.replacement617" "$helper"
printf 'first-selector617' > "$INVARIANT_FRESH_HELPER_EVENTS_617"
printf 'replacement-completed617'
`)
	write468(t, q.SourceDir, q.Directory+"/later617.sh", `set -eu
printf 'later-selector-ran617' > later-ran617
printf 'authentic-selector617' > "$INVARIANT_FRESH_HELPER_EVENTS_617"
printf 'authentic-diagnostics617'
exit 7
`)
	ctx, done := context.WithTimeout(t.Context(), 3*time.Minute)
	defer done()
	env, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := docker591Close(t, env); err != nil {
			t.Error("transport cleanup: ", err)
		}
		b.clean()
	})
	transport, ok := env.(RepositoryProcessTransport)
	if !ok {
		t.Fatalf("prepared %s environment lacks its process transport", language)
	}
	run := func(script string) (RepositoryProcessOutput, error) {
		p, err := transport.Prepare(ctx, RepositoryProcessSpec{Argv: []string{"/bin/sh", script}, EventEnv: freshEvent617})
		if err != nil {
			return RepositoryProcessOutput{}, err
		}
		process, err := p.Start(ctx)
		if err != nil {
			return RepositoryProcessOutput{}, err
		}
		return process.Wait(ctx)
	}
	first, err := run("replace617.sh")
	if err != nil || !first.Started || !first.Exited || !first.Drained || first.ExitCode != 0 || string(first.Events) != "first-selector617" || string(first.Stdout) != "replacement-completed617" {
		t.Fatalf("earlier workload must actually replace its running %s helper: %v %+v", language, err, first)
	}
	log, err := os.ReadFile(filepath.Join(b.root, "helper-invocations617"))
	if err != nil {
		t.Fatal(err)
	}
	paths := strings.Fields(string(log))
	if len(paths) != 1 {
		t.Fatalf("first selector did not have one helper invocation: %q", paths)
	}
	poisoned := docker591Map(filepath.Join(b.root, "containers", q.Token), paths[0])
	data, err := os.ReadFile(poisoned)
	if err != nil || !bytes.Equal(data, forgedTransport617(language)) {
		t.Fatalf("earlier workload left no replacement at its helper path: %v", err)
	}
	later, runErr := run("later617.sh")
	if ctx.Err() != nil {
		t.Fatal("timeout or cancellation cannot stand in for a tampering refusal: ", ctx.Err())
	}
	if bytes.Contains(later.Events, []byte("forged-selector")) || bytes.Contains(later.Stdout, []byte("forged-selector")) {
		t.Fatalf("later selector executed workload-supplied %s helper bytes: %v %+v", language, runErr, later)
	}
	if runErr != nil {
		// Detecting tampering and refusing the later invocation is safe. It
		// cannot leave an apparently completed successful selector behind.
		if later.Started && later.Exited && later.Drained && later.ExitCode == 0 {
			t.Fatalf("tampering refusal retained fabricated success: %+v", later)
		}
		return
	}
	if !later.Started || !later.Exited || !later.Drained || later.ExitCode != 7 || string(later.Events) != "authentic-selector617" || string(later.Stdout) != "authentic-diagnostics617" {
		t.Fatalf("later selector did not run under the authentic %s helper: %+v", language, later)
	}
	log, err = os.ReadFile(filepath.Join(b.root, "helper-invocations617"))
	if err != nil {
		t.Fatal(err)
	}
	paths = strings.Fields(string(log))
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatalf("each invocation needs a new private helper path: %q", paths)
	}
	marker := filepath.Join(b.root, "containers", q.Token, "invariant", "work", q.Directory, "later-ran617")
	if data, err := os.ReadFile(marker); err != nil || string(data) != "later-selector-ran617" {
		t.Fatalf("successful transport never ran the later workload: %v %q", err, data)
	}
}

func TestGoTransportFreshHelpers617(t *testing.T) {
	freshHelper617(t, "go")
}

func TestPytestTransportFreshHelpers617(t *testing.T) {
	freshHelper617(t, "python")
}
