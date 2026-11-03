package plumbing

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gitdek/invariant/internal/regular"
)

// DockerDependencyRunner keeps installed dependencies in immutable image
// layers. All writes, including compilation caches, belong to a disposable
// container. It mounts neither the Docker socket nor any host directory.
type DockerDependencyRunner struct{}

const dependencyLabel = "io.invariant.repository-dependencies"
const dependencyWork = "/invariant/work"

var dependencyContainer = regexp.MustCompile(`^invariant-deps-[a-zA-Z0-9-]+$`)
var dependencyBase = regexp.MustCompile(`^[^\s]+@sha256:[0-9a-f]{64}$`)

type dependencyImage struct {
	ID           string `json:"Id"`
	OS           string `json:"Os"`
	Architecture string
	Variant      string
	RepoDigests  []string
	Config       struct {
		Labels  map[string]string
		Volumes map[string]json.RawMessage
	}
	RootFS struct {
		Layers []string
	}
}

func dependencyDocker(ctx context.Context, args ...string) ([]byte, error) {
	return dependencyDockerInput(ctx, nil, args...)
}

func dependencyDockerInput(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = input
	// Container removal is owned by Cleanup, under its single reserve, not
	// by each client invocation's cancellation handler.
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil {
		return out, fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func inspectDependencyImage(ctx context.Context, image, platform string) (dependencyImage, error) {
	var info dependencyImage
	if !dependencyID.MatchString(image) && !dependencyBase.MatchString(image) {
		return info, errors.New("dependency images must be immutable IDs or digest-pinned bases")
	}
	out, err := dependencyDocker(ctx, "image", "inspect", image)
	if err != nil {
		return info, err
	}
	var images []dependencyImage
	if err := json.Unmarshal(out, &images); err != nil || len(images) != 1 {
		return info, errors.New("Docker returned invalid image inspection")
	}
	info = images[0]
	actual := info.OS + "/" + info.Architecture
	// Docker may report arm64's default variant as v8 or leave it empty.
	if info.Variant != "" && !(info.Architecture == "arm64" && info.Variant == "v8" && platform == "linux/arm64") {
		actual += "/" + info.Variant
	}
	if !dependencyID.MatchString(info.ID) || actual != platform || len(info.Config.Volumes) != 0 {
		return info, errors.New("dependency image identity/platform is invalid or it declares mutable volumes")
	}
	if dependencyID.MatchString(image) {
		if info.ID != image {
			return info, errors.New("Docker inspected a different immutable image")
		}
	} else {
		_, digest, _ := strings.Cut(image, "@")
		if !slices.ContainsFunc(info.RepoDigests, func(ref string) bool { return strings.HasSuffix(ref, "@"+digest) }) {
			return info, errors.New("Docker base inspection does not attest the requested digest")
		}
	}
	return info, nil
}

func (r *DockerDependencyRunner) Install(ctx context.Context, q DependencyRequest) (DependencyArtifact, error) {
	var a DependencyArtifact
	if err := validateDependencyRequest(q, true); err != nil {
		return a, err
	}
	base, err := inspectDependencyImage(ctx, q.Image, q.Platform)
	if err != nil && q.Pull && ctx.Err() == nil {
		if _, err := dependencyDocker(ctx, "pull", "--platform", q.Platform, q.Image); err != nil {
			return a, err
		}
		base, err = inspectDependencyImage(ctx, q.Image, q.Platform)
	}
	if err != nil {
		return a, err
	}
	if err := verifyDependencyInputs(ctx, q.SourceDir, q.Inputs); err != nil {
		return a, err
	}
	stage, err := stageDependencySource(ctx, q, true)
	if err != nil {
		return a, err
	}
	defer r.discard(q.Token, stage)
	// The base is resolved once. Docker cannot select another platform or
	// entrypoint after inspection, or pull an image at container creation.
	launch := cloneDependencyRequest(q)
	launch.Image = base.ID
	script := "set -eu\nmkdir -p /invariant/gomod /tmp/gocache /tmp/npm-cache\n"
	if len(q.Argv) == 0 {
		script += ":\n"
	} else {
		if q.Argv[0] == "python" || q.Argv[0] == "python3" {
			script += "\"$1\" -I -m venv /invariant/venv\nshift\nexec /invariant/venv/bin/python -I \"$@\"\n"
		} else {
			script += "exec \"$@\"\n"
		}
	}
	if err := createDependencyContainer(ctx, launch, script); err != nil {
		return a, err
	}
	if err := copyDependencyStage(ctx, stage, q.Token); err != nil {
		return a, err
	}
	if _, err := startDependencyContainer(ctx, q.Token); err != nil {
		return a, err
	}
	// Read the container's copies, not the staging directory it never mounted.
	// Evidence is added by the host after this check, outside repository control.
	checked, err := os.MkdirTemp("", "invariant-dependency-checked-")
	if err != nil {
		return a, err
	}
	defer os.RemoveAll(checked)
	for name := range q.Inputs {
		to := filepath.Join(checked, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return a, err
		}
		if _, err := dependencyDocker(ctx, "cp", q.Token+":"+path.Join(dependencyWork, name), to); err != nil {
			return a, err
		}
	}
	if err := verifyDependencyInputs(ctx, checked, q.Inputs); err != nil {
		return a, err
	}
	a = DependencyArtifact{Base: q.Image, BaseImage: base.ID, Platform: q.Platform, Key: q.Key, Inputs: maps.Clone(q.Inputs), Complete: true}
	evidence, err := json.Marshal(a)
	if err != nil {
		return DependencyArtifact{}, err
	}
	label := "LABEL " + dependencyLabel + "=" + strconv.Quote(base64.StdEncoding.EncodeToString(evidence))
	out, err := dependencyDocker(ctx, "commit", "--change", label, q.Token)
	if err != nil {
		return DependencyArtifact{}, err
	}
	a.Image = strings.TrimSpace(string(out))
	if !dependencyID.MatchString(a.Image) {
		return DependencyArtifact{}, errors.New("Docker commit did not return an immutable environment ID")
	}
	return a, ctx.Err()
}

// Inspect never pulls. It checks Docker's actual image and base identities,
// platforms and layer ancestry, then reads the host-sealed recipe evidence.
func (r *DockerDependencyRunner) Inspect(ctx context.Context, image, platform string) (DependencyArtifact, error) {
	var a DependencyArtifact
	if !dependencyID.MatchString(image) {
		return a, errors.New("dependency inspection requires an immutable environment ID")
	}
	info, err := inspectDependencyImage(ctx, image, platform)
	if err != nil {
		return a, err
	}
	evidence, err := base64.StdEncoding.DecodeString(info.Config.Labels[dependencyLabel])
	if err != nil || len(evidence) == 0 {
		return a, errors.New("dependency image has no valid preparation evidence")
	}
	if err := dependencyJSON(evidence, &a); err != nil {
		return a, err
	}
	if !a.Complete || a.Image != "" || a.Platform != platform || !dependencyID.MatchString(a.Key) || !dependencyID.MatchString(a.BaseImage) || a.Inputs == nil {
		return DependencyArtifact{}, errors.New("dependency image evidence is incomplete")
	}
	for name, hash := range a.Inputs {
		if !dependencyRelative(name, false) || !dependencyID.MatchString(hash) {
			return DependencyArtifact{}, errors.New("dependency image contains invalid input evidence")
		}
	}
	base, err := inspectDependencyImage(ctx, a.Base, platform)
	if err != nil {
		return DependencyArtifact{}, err
	}
	if base.ID != a.BaseImage || len(info.RootFS.Layers) < len(base.RootFS.Layers) || !slices.Equal(info.RootFS.Layers[:len(base.RootFS.Layers)], base.RootFS.Layers) {
		return DependencyArtifact{}, errors.New("dependency image does not derive from its recorded immutable base")
	}
	a.Image = info.ID
	return a, ctx.Err()
}

func (r *DockerDependencyRunner) Run(ctx context.Context, q DependencyRequest) ([]byte, error) {
	if err := validateDependencyRequest(q, false); err != nil {
		return nil, err
	}
	stage, err := stageDependencySource(ctx, q, false)
	if err != nil {
		return nil, err
	}
	defer r.discard(q.Token, stage)
	if err := createDependencyContainer(ctx, q, "set -eu\nexec \"$@\"\n"); err != nil {
		return nil, err
	}
	if err := copyDependencyStage(ctx, stage, q.Token); err != nil {
		return nil, err
	}
	return startDependencyContainer(ctx, q.Token)
}

func stageDependencySource(ctx context.Context, q DependencyRequest, install bool) (string, error) {
	// Copy only regular candidate files. No candidate-controlled symlink or
	// hard link can make Docker read outside the caller's candidate directory.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(q.SourceDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	stage, err := os.MkdirTemp("", "invariant-dependency-copy-")
	if err != nil {
		return "", err
	}
	// docker cp requires destination parents to exist, and create need not
	// create its workdir until start. Copy the complete layout into / instead.
	work := filepath.Join(stage, "invariant", "work")
	if err := os.MkdirAll(filepath.Join(work, filepath.FromSlash(q.Directory)), 0o755); err != nil {
		discardStage(stage)
		return "", err
	}
	// npm refuses loading the same configuration path twice, even /dev/null.
	// Both files are host-created outside the repository's copied subtree.
	for _, name := range []string{"npm-user.npmrc", "npm-global.npmrc"} {
		if err := os.WriteFile(filepath.Join(stage, "invariant", name), nil, 0o600); err != nil {
			discardStage(stage)
			return "", err
		}
	}
	err = copyDependencyTree(ctx, root, work, "", q.Inputs, install)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		discardStage(stage)
		return "", err
	}
	return stage, nil
}

// Stream the validated private copy with root ownership. Container root has
// no capabilities to bypass the host owner's permissions or chown the files.
func copyDependencyStage(ctx context.Context, stage, token string) error {
	input, output, err := os.Pipe()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		err := archiveDependencyStage(ctx, output, stage)
		output.Close()
		done <- err
	}()
	_, copyErr := dependencyDockerInput(ctx, input, "cp", "-", token+":/")
	// Close both ends before joining the writer: Docker may exit without
	// reading, or a canceled client's child may still hold its stdin open.
	input.Close()
	output.Close()
	archiveErr := <-done
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.Join(copyErr, archiveErr)
}

func archiveDependencyStage(ctx context.Context, dst io.Writer, stage string) error {
	root, err := os.OpenRoot(stage)
	if err != nil {
		return err
	}
	defer root.Close()
	archive := tar.NewWriter(dst)
	if err := archiveDependencyTree(ctx, archive, root, ""); err != nil {
		return err
	}
	return archive.Close()
}

func archiveDependencyTree(ctx context.Context, archive *tar.Writer, root *os.Root, prefix string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := path.Join(prefix, entry.Name())
			info, err := root.Lstat(entry.Name())
			if err != nil {
				return err
			}
			// Construct headers without host ownership or special permissions.
			header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
			if info.IsDir() {
				header.Typeflag = tar.TypeDir
			} else if info.Mode().IsRegular() {
				header.Typeflag, header.Size = tar.TypeReg, info.Size()
			} else {
				return &regular.Refused{File: name, Why: "it isn't a regular file or directory"}
			}
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				child, err := root.OpenRoot(entry.Name())
				if err != nil {
					return err
				}
				err = archiveDependencyTree(ctx, archive, child, name)
				closeErr := child.Close()
				if err := errors.Join(err, closeErr); err != nil {
					return err
				}
			} else {
				file, err := root.OpenFile(entry.Name(), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
				if err != nil {
					return err
				}
				err = copyDependencyBytes(ctx, archive, file, info.Size())
				closeErr := file.Close()
				if err := errors.Join(err, closeErr); err != nil {
					return err
				}
			}
		}
		if readErr == io.EOF {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
	}
}

// discard removes a check's or an installation's container, then its staged
// copy, within one cleanup allowance. The container goes first: deleting a
// big candidate's copy can take a while, and mustn't leave the container
// running meanwhile (#468's review). The caller's own cleanup then finds the
// container gone, and reports anything this one couldn't remove.
func (r *DockerDependencyRunner) discard(token, stage string) {
	ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer cancel()
	_ = r.Cleanup(ctx, token)
	removeStage(ctx, stage)
}

// discardStage deletes a staged copy that never reached a container, within
// a cleanup allowance of its own.
func discardStage(stage string) {
	ctx, cancel := context.WithTimeout(context.Background(), SandboxCleanupAllowance)
	defer cancel()
	removeStage(ctx, stage)
}

// removeStage deletes a staged copy, waiting for it no longer than ctx
// allows. Whatever is left then goes on being deleted in the background, so
// a big candidate can't hold cleanup past its reserve.
func removeStage(ctx context.Context, stage string) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		os.RemoveAll(stage)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Read directories in batches and files in chunks, checking the original
// context between operations. Neither a huge directory nor a growing file
// can force an unbounded read before cancellation is observed.
func copyDependencyTree(ctx context.Context, root *os.Root, to, prefix string, inputs map[string]string, install bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := path.Join(prefix, entry.Name())
			if !install && strings.EqualFold(entry.Name(), ".git") {
				continue
			}
			if install {
				_, take := inputs[name]
				for input := range inputs {
					take = take || strings.HasPrefix(input, name+"/")
				}
				if !take {
					continue
				}
			}
			info, err := root.Lstat(entry.Name())
			if err != nil {
				return err
			}
			destination := filepath.Join(to, entry.Name())
			if info.IsDir() {
				if err := copyDependencyDirectory(ctx, root, entry.Name(), destination, name, info, inputs, install); err != nil {
					return err
				}
			} else if err := copyDependencyFile(ctx, root, entry.Name(), destination, name, info); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return ctx.Err()
		}
		if readErr != nil {
			return readErr
		}
	}
}

func copyDependencyDirectory(ctx context.Context, parent *os.Root, entry, to, name string, before os.FileInfo, inputs map[string]string, install bool) error {
	root, err := parent.OpenRoot(entry)
	if err != nil {
		return err
	}
	defer root.Close()
	after, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return &regular.Refused{File: name, Why: "it changed while the factory read it"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	return copyDependencyTree(ctx, root, to, name, inputs, install)
}

func copyDependencyFile(ctx context.Context, root *os.Root, entry, to, name string, before os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return &regular.Refused{File: name, Why: "it isn't a regular file"}
	}
	// A swap to a pipe cannot block open; a swapped symlink cannot be followed.
	f, err := root.OpenFile(entry, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(before, info) || !info.Mode().IsRegular() {
		return &regular.Refused{File: name, Why: "it changed while the factory read it"}
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
		return &regular.Refused{File: name, Why: "it's a hard link to another file"}
	}
	// Preserve ordinary permissions so direct execution checks the candidate's
	// executable bits. Special permissions never survive into the private copy.
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = copyDependencyBytes(ctx, out, f, info.Size())
	if err == nil {
		err = out.Chmod(info.Mode().Perm())
	}
	closeErr := out.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return err
	}
	after, err := f.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return &regular.Refused{File: name, Why: "it changed while the factory read it"}
	}
	return ctx.Err()
}

func copyDependencyBytes(ctx context.Context, dst io.Writer, src io.Reader, size int64) error {
	buf := make([]byte, 64<<10)
	for size > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf[:min(int64(len(buf)), size)])
		if err := ctx.Err(); err != nil {
			return err
		}
		if n > 0 {
			written, err := dst.Write(buf[:n])
			if err != nil {
				return err
			}
			if written != n {
				return io.ErrShortWrite
			}
			size -= int64(n)
		}
		if readErr != nil && size != 0 {
			if readErr == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return readErr
		}
		if n == 0 && readErr == nil {
			return io.ErrNoProgress
		}
	}
	return ctx.Err()
}

func createDependencyContainer(ctx context.Context, q DependencyRequest, script string) error {
	network := "none"
	if q.Network {
		network = "bridge"
	}
	args := []string{"create", "--name", q.Token, "--init", "--network", network, "--pull", "never",
		"--platform", q.Platform, "--hostname", "localhost", "--memory", "4g", "--pids-limit", "1024",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "0:0",
		"--entrypoint", "/usr/bin/env", "--workdir", path.Join(dependencyWork, q.Directory)}
	env := maps.Clone(q.Env)
	if env == nil {
		env = map[string]string{}
	}
	if env["PATH"] == "" {
		env["PATH"] = dependencyEnv("", false)["PATH"]
	}
	env["HOME"] = "/tmp"
	// docker create, like run, applies client proxy defaults. Explicit empty
	// values suppress them; env -i also removes the base image's environment.
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "FTP_PROXY", "ftp_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		env[name] = ""
	}
	keys := slices.Sorted(maps.Keys(env))
	for _, name := range keys {
		args = append(args, "--env", name+"="+env[name])
	}
	args = append(args, q.Image, "-i")
	for _, name := range keys {
		args = append(args, name+"="+env[name])
	}
	args = append(args, "/bin/sh", "-c", script, "invariant-dependencies")
	args = append(args, q.Argv...)
	_, err := dependencyDocker(ctx, args...)
	return err
}

func startDependencyContainer(ctx context.Context, token string) ([]byte, error) {
	out, err := dependencyDocker(ctx, "start", "--attach", token)
	if err != nil {
		return out, err
	}
	status, err := dependencyDocker(ctx, "wait", token)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(string(status)) != "0" {
		return out, fmt.Errorf("dependency container exited with status %s: %s", strings.TrimSpace(string(status)), strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (r *DockerDependencyRunner) Cleanup(ctx context.Context, token string) error {
	if !dependencyContainer.MatchString(token) {
		return errors.New("invalid dependency cleanup token")
	}
	out, err := dependencyDocker(ctx, "rm", "--force", "--volumes", token)
	if err != nil && (strings.Contains(string(out), "No such container") || strings.Contains(string(out), "No such object")) {
		return nil
	}
	return err
}

func validateDependencyRequest(q DependencyRequest, install bool) error {
	if !dependencyContainer.MatchString(q.Token) || !dependencyPlatform.MatchString(q.Platform) || !dependencyRelative(q.Directory, true) || q.SourceDir == "" || !q.Init || q.MemoryBytes != 4<<30 || q.PidsLimit != 1024 {
		return errors.New("invalid dependency container request")
	}
	if install {
		if !dependencyBase.MatchString(q.Image) || !dependencyID.MatchString(q.Key) || q.Inputs == nil || q.Network != (len(q.Argv) != 0) {
			return errors.New("invalid dependency installation request")
		}
	} else if !dependencyID.MatchString(q.Image) || q.Network || q.Pull || len(q.Argv) == 0 {
		return errors.New("dependency checks require an offline immutable environment")
	}
	for _, arg := range q.Argv {
		if strings.ContainsRune(arg, 0) {
			return errors.New("dependency command contains NUL")
		}
	}
	if len(q.Argv) != 0 && (q.Argv[0] == "" || strings.HasPrefix(q.Argv[0], "-")) {
		return errors.New("invalid dependency executable")
	}
	allowed := dependencyEnv("go", true)
	maps.Copy(allowed, dependencyEnv("python", false))
	maps.Copy(allowed, dependencyEnv("typescript", false))
	for key, value := range q.Env {
		if _, ok := allowed[key]; !ok || strings.ContainsRune(value, 0) {
			return fmt.Errorf("unsupported dependency environment variable %q", key)
		}
	}
	for name, hash := range q.Inputs {
		if !dependencyRelative(name, false) || !dependencyID.MatchString(hash) {
			return errors.New("invalid dependency input")
		}
	}
	return nil
}

func dependencyRelative(name string, root bool) bool {
	if name == "." {
		return root
	}
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:\x00\r\n") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." || strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}
