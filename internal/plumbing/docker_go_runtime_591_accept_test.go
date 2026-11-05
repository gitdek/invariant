package plumbing

// This fixture replaces the Docker CLI, not selection, discovery, compilation,
// the testing hooks, the transport helper, or the collector. Container paths
// are translated into private directories and commands run with real Go.
// Its Go cache is test-owned; production Docker arguments must never mount it.
import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/repository"
)

const docker591Switch = "--invariant-docker-boundary-591"

func init() {
	if len(os.Args) >= 5 && os.Args[1] == docker591Switch {
		if err := docker591Command(os.Args[2], os.Args[3], os.Args[4:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			var e *exec.ExitError
			if errors.As(err, &e) && e.ExitCode() > 0 {
				os.Exit(e.ExitCode())
			}
			os.Exit(125)
		}
		os.Exit(0)
	}
}

type docker591Call struct {
	Args []string
	At   int64
	Done bool
}
type docker591Container struct {
	Args             []string
	Directory, Image string
}
type docker591Boundary struct {
	t                *testing.T
	root, goexe, bin string
	hostTmp          string
}

func docker591JSON(name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	return os.WriteFile(name, b, 0600)
}
func docker591ReadJSON(name string, value any) error {
	b, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}
func docker591Map(root, s string) string {
	// Only virtual absolute paths are translated; selectors and other arguments
	// retain their bytes. The fake's path namespace never reaches production.
	s = strings.NewReplacer("/invariant/", root+"/invariant/", "/tmp/", root+"/tmp/").Replace(s)
	if s == "/tmp" {
		s = root + "/tmp"
	}
	return s
}
func docker591Archive(dst string, input io.Reader) error {
	r := tar.NewReader(input)
	for {
		h, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(path.Clean(h.Name), "./")
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || h.Uid != 0 || h.Gid != 0 {
			return fmt.Errorf("unsafe Docker copy header: %+v", h)
		}
		to := filepath.Join(dst, filepath.FromSlash(name))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(to, os.FileMode(h.Mode)); err != nil {
				return err
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("nonregular Docker input %s", name)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(to, b, os.FileMode(h.Mode)); err != nil {
			return err
		}
	}
}
func docker591Copy(from, to string) error {
	i, err := os.Stat(from)
	if err != nil {
		return err
	}
	if i.IsDir() {
		if err := os.MkdirAll(to, i.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := docker591Copy(filepath.Join(from, e.Name()), filepath.Join(to, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	return os.WriteFile(to, b, i.Mode().Perm())
}
func docker591FixOverlays(root string) error {
	// JSON overlay paths belong to the simulated filesystem, just like argv.
	// Candidate files, pinned sources and host helper source bytes are untouched.
	return filepath.WalkDir(filepath.Join(root, "invariant"), func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, filepath.Join(root, "invariant", "work")+"/") {
			return nil
		}
		var overlay struct{ Replace map[string]string }
		if docker591ReadJSON(name, &overlay) != nil || len(overlay.Replace) == 0 {
			return nil
		}
		for k, v := range overlay.Replace {
			if strings.HasPrefix(v, "/invariant/") {
				overlay.Replace[k] = docker591Map(root, v)
			}
		}
		return docker591JSON(name, overlay)
	})
}
func docker591Command(state, goexe string, argv []string) (ret error) {
	if len(argv) == 0 {
		return errors.New("empty Docker invocation")
	}
	stamp := fmt.Sprintf("%020d-%d", time.Now().UnixNano(), os.Getpid())
	log := filepath.Join(state, "calls", stamp)
	if err := docker591JSON(log+"-begin.json", docker591Call{argv, time.Now().UnixNano(), false}); err != nil {
		return err
	}
	defer func() { _ = docker591JSON(log+"-done.json", docker591Call{argv, time.Now().UnixNano(), true}) }()
	container := func(token string) string { return filepath.Join(state, "containers", token) }
	baseID := dependencyHash([]byte("prepared base 591"))
	image := func(id string) map[string]any {
		return map[string]any{"Id": id, "OS": "linux", "Architecture": "amd64", "RepoDigests": []string{image468}, "Config": map[string]any{"Labels": map[string]string{}}, "RootFS": map[string]any{"Layers": []string{dependencyHash([]byte("base layer 591"))}}}
	}
	switch argv[0] {
	case "image":
		if len(argv) != 3 || argv[1] != "inspect" {
			return fmt.Errorf("unexpected image command %q", argv)
		}
		value := image(baseID)
		if argv[2] != image468 && argv[2] != baseID {
			if err := docker591ReadJSON(filepath.Join(state, "images", strings.TrimPrefix(argv[2], "sha256:")+".json"), &value); err != nil {
				return err
			}
		}
		return json.NewEncoder(os.Stdout).Encode([]any{value})
	case "create":
		token := flag484(argv, "--name")
		if !dependencyContainer.MatchString(token) {
			return errors.New("missing private container name")
		}
		at := -1
		for i, s := range argv {
			if dependencyID.MatchString(s) {
				at = i
				break
			}
		}
		if at < 0 {
			return errors.New("create did not use immutable image ID")
		}
		root := container(token)
		if err := os.MkdirAll(filepath.Join(root, "invariant"), 0700); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(root, "tmp", ".config", "go", "telemetry"), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "tmp", ".config", "go", "telemetry", "mode"), []byte("off\n"), 0600); err != nil {
			return err
		}
		if err := docker591JSON(filepath.Join(root, "container.json"), docker591Container{argv[at+1:], flag484(argv, "--workdir", "-w"), argv[at]}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, token)
		return nil
	case "cp":
		if len(argv) != 3 {
			return fmt.Errorf("unexpected copy %q", argv)
		}
		if token, name, ok := strings.Cut(argv[1], ":"); ok && dependencyContainer.MatchString(token) {
			from := filepath.Join(container(token), strings.TrimPrefix(name, "/"))
			info, err := os.Lstat(from)
			if err != nil {
				return err
			}
			if argv[2] != "-" {
				return docker591Copy(from, argv[2])
			}
			w := tar.NewWriter(os.Stdout)
			h, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			h.Name = filepath.Base(from)
			if err = w.WriteHeader(h); err != nil {
				return err
			}
			b, err := os.ReadFile(from)
			if err != nil {
				return err
			}
			if _, err = w.Write(b); err != nil {
				return err
			}
			return w.Close()
		}
		token, name, ok := strings.Cut(argv[2], ":")
		if !ok || !dependencyContainer.MatchString(token) {
			return errors.New("copy lacks container destination")
		}
		root := container(token)
		to := filepath.Join(root, strings.TrimPrefix(name, "/"))
		if argv[1] == "-" {
			if err := docker591Archive(to, os.Stdin); err != nil {
				return err
			}
		} else {
			if err := docker591Copy(argv[1], to); err != nil {
				return err
			}
		}
		return docker591FixOverlays(root)
	case "start":
		// Empty-graph preparation is the only attached start; persistent checks
		// have a fixed idle process and do their work through exec.
		return nil
	case "wait":
		fmt.Fprintln(os.Stdout, "0")
		return nil
	case "commit":
		label := flag484(argv, "--change")
		prefix := "LABEL " + dependencyLabel + "="
		if !strings.HasPrefix(label, prefix) {
			return errors.New("missing host preparation seal")
		}
		value, err := strconv.Unquote(strings.TrimPrefix(label, prefix))
		if err != nil {
			return err
		}
		id := dependencyHash([]byte(label))
		v := image(id)
		v["Config"] = map[string]any{"Labels": map[string]string{dependencyLabel: value}}
		v["RootFS"] = map[string]any{"Layers": []string{dependencyHash([]byte("base layer 591")), dependencyHash([]byte(label))}}
		if err := docker591JSON(filepath.Join(state, "images", strings.TrimPrefix(id, "sha256:")+".json"), v); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, id)
		return nil
	case "exec":
		at := -1
		for i, a := range argv {
			if dependencyContainer.MatchString(a) {
				at = i
				break
			}
		}
		if at < 0 || at+1 == len(argv) {
			return errors.New("exec without private container")
		}
		token := argv[at]
		root := container(token)
		var c docker591Container
		if err := docker591ReadJSON(filepath.Join(root, "container.json"), &c); err != nil {
			return err
		}
		dir := flag484(argv[:at], "--workdir", "-w")
		if dir == "" {
			dir = c.Directory
		}
		args := slices.Clone(argv[at+1:])
		env := map[string]string{}
		if len(args) < 2 || args[0] != "/usr/bin/env" || args[1] != "-i" {
			return errors.New("Docker exec did not clear image environment")
		}
		args = args[2:]
		for len(args) > 0 {
			k, v, ok := strings.Cut(args[0], "=")
			if !ok {
				break
			}
			env[k] = docker591Map(root, v)
			args = args[1:]
		}
		if len(args) == 0 {
			return errors.New("no exec command")
		}
		for i, a := range args {
			args[i] = docker591Map(root, a)
		}
		if runtime.GOOS == "darwin" {
			for i, a := range args {
				args[i] = strings.ReplaceAll(a, "stat -c %h --", "stat -f %l")
			}
		}
		env["PATH"] = filepath.Dir(goexe) + ":/usr/bin:/bin"
		env["TMPDIR"] = filepath.Join(root, "tmp")
		// Sharing only this fixture's disposable compiler cache keeps the fake
		// fast. The assertions below inspect the actual container cache policy.
		env["GOCACHE"] = filepath.Join(state, "go-cache")
		env["PWD"] = docker591Map(root, dir)
		if err := os.MkdirAll(env["GOCACHE"], 0700); err != nil {
			return err
		}
		runtimeCall := slices.Contains(args, "-test.paniconexit0")
		mode, _ := os.ReadFile(filepath.Join(state, "fault"))
		if runtimeCall && string(mode) == "start" {
			return errors.New("injected Docker runtime startup failure 591")
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = env["PWD"]
		cmd.Stdin = os.Stdin
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := localProcessReaper(); err != nil {
			return err
		}
		var captured bytes.Buffer
		if runtimeCall && string(mode) == "truncate" {
			cmd.Stdout = &captured
		} else {
			cmd.Stdout = os.Stdout
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		pidFile := filepath.Join(state, "pids", token+"-"+strconv.Itoa(cmd.Process.Pid))
		if err := os.MkdirAll(filepath.Dir(pidFile), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
			return err
		}
		err := cmd.Wait()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		reapErr := localReapGroup(cleanup, cmd.Process.Pid)
		done()
		_ = os.Remove(pidFile)
		if runtimeCall && string(mode) == "truncate" {
			b := captured.Bytes()
			if len(b) > 0 {
				b = b[:len(b)-1]
			}
			_, _ = os.Stdout.Write(b)
		}
		if runtimeCall && string(mode) == "transport" {
			return errors.New("injected Docker transport failure after terminal 591")
		}
		return errors.Join(err, reapErr)
	case "rm":
		token := argv[len(argv)-1]
		root := container(token)
		if !dependencyContainer.MatchString(token) {
			return errors.New("unsafe removal")
		}
		entries, _ := os.ReadDir(filepath.Join(state, "pids"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), token+"-") {
				b, _ := os.ReadFile(filepath.Join(state, "pids", e.Name()))
				pid, _ := strconv.Atoi(string(b))
				if pid > 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			}
		}
		if _, err := os.Stat(filepath.Join(state, "hold-cleanup")); err == nil {
			_ = os.WriteFile(filepath.Join(state, "cleanup-entered"), []byte(token), 0600)
			limit := time.Now().Add(8 * time.Second)
			for time.Now().Before(limit) {
				if _, err := os.Stat(filepath.Join(state, "release-cleanup")); err == nil {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
		if err := os.RemoveAll(root); err != nil {
			return err
		}
		mode, _ := os.ReadFile(filepath.Join(state, "fault"))
		if string(mode) == "cleanup" {
			return errors.New("injected container cleanup failure 591")
		}
		return nil
	default:
		return fmt.Errorf("Docker fixture refuses acquisition or unimplemented command: %q", argv)
	}
}

func newDocker591(t *testing.T) *docker591Boundary {
	t.Helper()
	exe, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("default acceptance requires Go: ", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	b := &docker591Boundary{t: t, root: t.TempDir(), goexe: exe, bin: t.TempDir()}
	b.hostTmp = filepath.Join(b.root, "host-staging")
	if err := os.MkdirAll(b.hostTmp, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", b.hostTmp)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(self) + " " + quote(docker591Switch) + " " + quote(b.root) + " " + quote(exe) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(b.bin, "docker"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", b.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AWS_SECRET_ACCESS_KEY", "host-secret-591")
	t.Setenv("GOFLAGS", "-exec=forged-host-command-591")
	t.Cleanup(func() {
		entries, _ := os.ReadDir(filepath.Join(b.root, "pids"))
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(b.root, "pids", e.Name()))
			pid, _ := strconv.Atoi(string(data))
			if pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
	})
	return b
}
func (b *docker591Boundary) calls() []docker591Call {
	b.t.Helper()
	entries, err := os.ReadDir(filepath.Join(b.root, "calls"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		b.t.Fatal(err)
	}
	var calls []docker591Call
	for _, e := range entries {
		var c docker591Call
		if err := docker591ReadJSON(filepath.Join(b.root, "calls", e.Name()), &c); err != nil {
			b.t.Fatal(err)
		}
		calls = append(calls, c)
	}
	slices.SortFunc(calls, func(a, b docker591Call) int {
		if a.At < b.At {
			return -1
		}
		if a.At > b.At {
			return 1
		}
		return 0
	})
	return calls
}
func (b *docker591Boundary) fault(mode string) {
	b.t.Helper()
	if err := os.WriteFile(filepath.Join(b.root, "fault"), []byte(mode), 0600); err != nil {
		b.t.Fatal(err)
	}
}
func (b *docker591Boundary) clean() {
	b.t.Helper()
	entries, err := os.ReadDir(filepath.Join(b.root, "containers"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		b.t.Fatal(err)
	}
	if len(entries) > 0 {
		b.t.Fatalf("containers survived: %v", entries)
	}
	entries, _ = os.ReadDir(filepath.Join(b.root, "pids"))
	if len(entries) > 0 {
		b.t.Fatalf("Docker client returned before joining its processes: %v", entries)
	}
	entries, err = os.ReadDir(b.hostTmp)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, entry := range entries {
		// Subtests retain their own testing.T temporary roots until cleanup;
		// all storage allocated by production must already have been removed.
		if !strings.HasPrefix(entry.Name(), "Test") {
			b.t.Fatalf("private host staging survived environment cleanup: %s", entry.Name())
		}
	}
}

func docker591Part(dir string) repository.Part {
	p := part468("go", dir)
	p.Name = dir
	p.Inputs = []string{}
	p.Lockfiles = []string{}
	p.Commands["install"] = repository.Command{NotApplicable: "The prepared fixture has only the standard library."}
	p.Commands["test_one"] = repository.Command{Argv: []string{"go", "test", "./...", "-run", "^{test}$", "-count=1", "-v=false", "-timeout=0", "-mod=readonly", "-p=1"}}
	return p
}
func docker591Source(body string) string {
	return "package fixture\nimport(\"testing\";\"os\";\"fmt\";\"time\")\nvar _=os.Exit;var _=fmt.Println;var _=time.Second\nfunc TestReady(t *testing.T){" + body + "}\n"
}
func docker591Open(t *testing.T, ctx context.Context, source, selector string, value int) (RepositoryProcessTransport, RepositoryAcceptanceEnvironment, repository.Part, RepositoryAcceptanceInvocation, DependencyRequest) {
	return docker591OpenImage(t, ctx, source, selector, value, dependencyHash([]byte("prepared image 591")), "linux/amd64")
}
func docker591OpenImage(t *testing.T, ctx context.Context, source, selector string, value int, image, platform string) (RepositoryProcessTransport, RepositoryAcceptanceEnvironment, repository.Part, RepositoryAcceptanceInvocation, DependencyRequest) {
	t.Helper()
	root := t.TempDir()
	part := docker591Part("api")
	write468(t, root, "api/go.mod", "module example.invalid/docker591\n\ngo 1.27.1\n")
	write468(t, root, "api/nested/value.go", fmt.Sprintf("package fixture\nconst Value=%d\n", value))
	write468(t, root, "api/nested/runtime_accept_test.go", source)
	test := Test{Name: "display only", Part: part.Name, Framework: "go", File: "api/nested/runtime_accept_test.go", Selector: selector}
	selection, err := (GoRepositoryAcceptanceAdapter{}).Select(ctx, part, test)
	if err != nil {
		t.Fatal(err)
	}
	in := repositoryInvocation(&Plan{Sources: map[string]string{test.File: source}, Execution: &Execution{Digest: dependencyHash([]byte("captured authority 591"))}}, test, selection)
	q := DependencyRequest{Token: "invariant-deps-591-" + in.Binding.InvocationID, Image: image, Platform: platform, Directory: part.Directory, SourceDir: root, Env: dependencyEnv("go", false), Init: true, MemoryBytes: 4 << 30, PidsLimit: 1024}
	e, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
		defer done()
		_ = e.Close(c)
	})
	transport, ok := e.(RepositoryProcessTransport)
	if !ok {
		t.Fatal("prepared Docker Go environment has no process transport")
	}
	acceptance, ok := e.(RepositoryAcceptanceEnvironment)
	if !ok {
		t.Fatal("prepared Docker Go environment has no RunAcceptance")
	}
	discovery, err := (GoRepositoryAcceptanceAdapter{}).Discover(ctx, part, in, e)
	if err != nil {
		t.Fatal("real prepared discovery: ", err)
	}
	in.Binding = discovery.Binding
	return transport, acceptance, part, in, q
}
func docker591Complete(t *testing.T, in RepositoryAcceptanceInvocation, e RepositoryAcceptanceEvidence, err error, status, reason string) {
	t.Helper()
	if err != nil || !e.Complete {
		t.Fatalf("runtime not complete: %v %+v", err, e)
	}
	selection, running, terminal := 0, 0, 0
	for _, r := range e.Records {
		if r.Binding != in.Binding {
			t.Fatalf("transport changed binding: %+v vs %+v", r.Binding, in.Binding)
		}
		if r.Kind == "selection" {
			selection++
		}
		if r.Kind == "test" && r.Status == "running" {
			running++
		}
		if r.Kind == "test" && r.Status == status {
			terminal++
			if !strings.Contains(r.Reason, reason) {
				t.Fatalf("lost reason: %+v", r)
			}
		}
	}
	if selection != 1 || running != 1 || terminal != 1 {
		t.Fatalf("want one selection/start/%s: %+v", status, e)
	}
}
func docker591Close(t *testing.T, e RepositoryCheckEnvironment) error {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer done()
	return e.Close(ctx)
}
func (b *docker591Boundary) policy(q DependencyRequest, in RepositoryAcceptanceInvocation) {
	b.t.Helper()
	create, compile, runtimeCall, reader := false, false, false, false
	for _, c := range b.calls() {
		if c.Done || len(c.Args) == 0 {
			continue
		}
		a := c.Args
		if a[0] == "pull" || a[0] == "build" || a[0] == "commit" {
			b.t.Fatalf("runtime acquired/published an image: %q", a)
		}
		for _, s := range a {
			if s == "--mount" || s == "--volume" || s == "-v" || s == "--privileged" || strings.HasPrefix(s, "--mount=") || strings.Contains(s, "host-secret-591") || strings.Contains(s, "forged-host-command-591") || strings.Contains(s, "python") {
				b.t.Fatalf("host/runtime authority leak: %q", a)
			}
		}
		if !slices.Contains(a, q.Token) {
			continue
		}
		if a[0] == "create" {
			create = true
			for k, v := range map[string]string{"--network": "none", "--pull": "never", "--platform": q.Platform, "--workdir": "/invariant/work/api", "--pids-limit": "1024", "--cap-drop": "ALL", "--security-opt": "no-new-privileges", "--hostname": "localhost", "--entrypoint": "/usr/bin/env", "--user": "0:0"} {
				if flag484(a, k) != v {
					b.t.Fatalf("lost %s=%s: %q", k, v, a)
				}
			}
			if !slices.Contains(a, q.Image) || !slices.Contains(a, "--init") || flag484(a, "--memory") != "4g" {
				b.t.Fatalf("wrong image/limits: %q", a)
			}
		}
		if a[0] != "exec" {
			continue
		}
		for k, v := range map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOWORK": "off", "GOCACHE": "/tmp/gocache", "GOMODCACHE": "/invariant/gomod"} {
			if !slices.Contains(a, k+"="+v) {
				b.t.Fatalf("lost offline policy %s: %q", k, a)
			}
		}
		if i := slices.Index(a, "go"); i >= 0 && i+1 < len(a) {
			if a[i+1] == "install" || a[i+1] == "get" || a[i+1] == "mod" {
				b.t.Fatalf("runtime installed dependencies: %q", a)
			}
			if a[i+1] == "run" {
				reader = true
			}
			if a[i+1] == "test" && slices.Contains(a[i:], "-c") {
				compile = true
				for _, arg := range in.Argv[2:] {
					if !slices.Contains(a[i:], arg) {
						b.t.Fatalf("lost selected compiler argument %q: %q", arg, a)
					}
				}
			}
		}
		if slices.Contains(a, "-test.paniconexit0") {
			runtimeCall = true
			if flag484(a, "--workdir", "-w") != "/invariant/work/api/nested" {
				b.t.Fatalf("runtime directory: %q", a)
			}
			for i, s := range a {
				if s == "-c" && i+1 < len(a) && strings.Contains(a[i+1], "touch_PWN") {
					b.t.Fatal("selector interpolated into shell source")
				}
			}
			i := slices.IndexFunc(a, func(s string) bool { return strings.HasSuffix(s, "/acceptance.test") })
			if i < 0 || !slices.Equal(a[i:], goTestingArguments(a[i], in.Argv)) {
				b.t.Fatalf("runtime changed literal selection or captured flags: %q", a)
			}
		}
	}
	if !create || !compile || !reader || !runtimeCall {
		b.t.Fatalf("missing real runtime boundary calls: create=%v compile=%v reader=%v runtime=%v", create, compile, reader, runtimeCall)
	}
}

func TestDockerGoPreparedRuntime591(t *testing.T) {
	b := newDocker591(t)
	ctx, done := context.WithTimeout(t.Context(), 3*time.Minute)
	defer done()
	source := strings.ReplaceAll(docker591Source(`if Value!=7{t.Fatal("needs seven 591")};t.Log("completed seven 591")`), "\n", "\r\n")
	var previous RepositoryAcceptanceInvocation
	for _, value := range []int{0, 7} {
		e, a, _, in, q := docker591Open(t, ctx, source, "TestReady", value)
		evidence, out, err := a.RunAcceptance(ctx, in)
		status, reason := "failed", "needs seven 591"
		if value == 7 {
			status, reason = "passed", "completed seven 591"
		}
		docker591Complete(t, in, evidence, err, status, reason)
		if in.Binding.Package != "example.invalid/docker591/nested" || in.Binding.SourceDigest != dependencyHash([]byte(source)) || in.Source != source {
			t.Fatal("lost real package or verbatim source binding")
		}
		if value == 7 {
			if previous.Binding.InvocationID == in.Binding.InvocationID {
				t.Fatal("reused invocation")
			}
			old, new := previous.Binding, in.Binding
			old.InvocationID = ""
			new.InvocationID = ""
			if old != new {
				t.Fatal("production-only correction changed acceptance authority")
			}
		}
		previous = in
		if value == 7 && len(out) == 0 {
			t.Fatal("lost ordinary diagnostics")
		}
		b.policy(q, in)
		if err := docker591Close(t, e); err != nil {
			t.Fatal(err)
		}
		if err := docker591Close(t, e); err != nil {
			t.Fatal("non-idempotent close: ", err)
		}
		if _, err := e.Run(ctx, []string{"go", "version"}); err == nil {
			t.Fatal("closed environment ran a command")
		}
		b.clean()
	}
	selector := "TestReady/literal.[x]+;$(touch_PWN)`literal`"
	source = docker591Source("t.Run(" + strconv.Quote(strings.TrimPrefix(selector, "TestReady/")) + ",func(t *testing.T){t.Log(\"literal child 591\")})")
	e, a, _, in, q := docker591Open(t, ctx, source, selector, 7)
	ev, _, err := a.RunAcceptance(ctx, in)
	docker591Complete(t, in, ev, err, "passed", "literal child 591")
	b.policy(q, in)
	for _, name := range []string{"../escape", "/outside", ".git/config", "a/../../escape", "a\\escape"} {
		if p, err := e.Stage(ctx, name, []byte("bad")); err == nil || p != "" {
			t.Fatalf("unsafe helper path accepted: %q", name)
		}
	}
	helper, err := e.Stage(ctx, "host-owned/probe", []byte("host helper 591"))
	if err != nil {
		t.Fatal(err)
	}
	if !path.IsAbs(helper) || strings.HasPrefix(helper, "/invariant/work/") {
		t.Fatal("helper lives in candidate tree: ", helper)
	}
	opaque := `package main
import("os";"fmt")
func main(){f,err:=os.OpenFile(os.Getenv("OPAQUE591"),os.O_WRONLY,0);if err!=nil{panic(err)};f.Write([]byte{0,1,255,10});f.Close();fmt.Print("stdout591\x00--- FAIL: TestReady\n");fmt.Fprint(os.Stderr,"stderr591\x00--- PASS: TestReady\n");os.Exit(7)}
`
	main, err := e.Stage(ctx, "opaque591/main.go", []byte(opaque))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := e.Stage(ctx, "opaque591/bytes.bin", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := e.Run(ctx, []string{"go", "build", "-o", binary, main}); err != nil {
		t.Fatalf("compile opaque probe: %v %s", err, out)
	}
	prepared, err := e.Prepare(ctx, RepositoryProcessSpec{Argv: []string{binary}, EventEnv: "OPAQUE591", Directory: "nested"})
	if err != nil {
		t.Fatal(err)
	}
	process, err := prepared.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	output, err := process.Wait(ctx)
	if err != nil || !output.Started || !output.Exited || !output.Drained || output.ExitCode != 7 || !bytes.Equal(output.Events, []byte{0, 1, 255, 10}) || string(output.Stdout) != "stdout591\x00--- FAIL: TestReady\n" || string(output.Stderr) != "stderr591\x00--- PASS: TestReady\n" {
		t.Fatalf("transport interpreted or merged opaque channels/status: %v %+v", err, output)
	}
	snapshot := process.Snapshot()
	snapshot.Events[0] = 9
	snapshot.Stdout[0] = '!'
	if process.Snapshot().Events[0] != 0 || process.Snapshot().Stdout[0] == '!' {
		t.Fatal("snapshot aliases transport storage")
	}
	if err := prepared.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []RepositoryProcessSpec{{Argv: []string{"go", "version"}, Env: map[string]string{"GOPROXY": "https://forged.invalid"}}, {Argv: []string{"go", "version"}, EventEnv: "GOWORK"}, {Argv: []string{"go", "version"}, Directory: "../escape"}} {
		if _, err := e.Prepare(ctx, spec); err == nil {
			t.Fatal("process spec overrode policy or escaped its part")
		}
	}
	if err := docker591Close(t, e); err != nil {
		t.Fatal(err)
	}
	b.clean()
	// Adding the lazy Go capability must not force a helper onto non-Go Run.
	q = request484(t, "typescript")
	nonGo, err := (&DockerDependencyRunner{}).OpenChecks(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	out, err := nonGo.Run(ctx, []string{"/bin/sh", "-c", "printf non-Go-591"})
	if err != nil || string(out) != "non-Go-591" {
		t.Fatalf("non-Go Run changed: %v %q", err, out)
	}
	if err := docker591Close(t, nonGo); err != nil {
		t.Fatal(err)
	}
	b.clean()
}
