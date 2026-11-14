package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gitdek/invariant/internal/conformance"
	"github.com/gitdek/invariant/internal/project"
)

// A recording owns its directory, but holds no descriptor while other gate
// checks run. Producers move their output here before removing their sandbox.
type recording struct {
	dir, path string
}

type recordedEvidence struct {
	Evidence
	recording *recording
	replay    *commandReplaySource
}

type recordingLanguage interface {
	checkRecording(context.Context, *project.Project) (Build, recordedEvidence, error)
}

func checkRecorded(ctx context.Context, lang Language, p *project.Project) (Build, recordedEvidence, error) {
	if l, ok := lang.(recordingLanguage); ok {
		return l.checkRecording(ctx, p)
	}
	b, e, err := lang.Check(ctx, p)
	return b, recordedEvidence{Evidence: e}, err
}

func takeRecording(ctx context.Context, path string) (r *recording, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The producer owns the recording, and may have left a link or a pipe in
	// its place. It's opened through its own directory, so a link out of
	// that directory is never followed (#816).
	f, err := openSandboxOutput(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer func() {
		err = errors.Join(err, f.Close())
		if err != nil {
			err = errors.Join(err, r.close())
			r = nil
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("recording %s is not a regular file", path)
	}
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "invariant-recording-")
	if err != nil {
		return nil, err
	}
	r = &recording{dir: dir, path: filepath.Join(dir, "traces.json")}
	if entry.Mode()&os.ModeSymlink == 0 {
		err = os.Rename(path, r.path)
	} else {
		// ReadFile followed links. Preserve those bytes without leaving a
		// link whose target disappears when the producer's workspace does.
		out, e := os.OpenFile(r.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return r, e
		}
		_, err = io.Copy(&recordingWriter{ctx: ctx, out: out}, f)
		err = errors.Join(err, out.Close())
	}
	return r, errors.Join(err, ctx.Err())
}

func (r *recording) close() error {
	if r == nil || r.dir == "" {
		return nil
	}
	err := os.RemoveAll(r.dir)
	if err == nil {
		r.dir = ""
	}
	return err
}

func (r *recording) check(ctx context.Context, runner conformance.Runner, dir, module string, bounds map[string]string, vars []string, modelStates int64, model *conformance.Model) (result conformance.Result, err error) {
	defer func() { err = errors.Join(err, r.close()) }()
	f, err := os.Open(r.path)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return conformance.CheckReader(ctx, runner, dir, module, bounds, vars, modelStates, f, model)
}

// Only explicit compatibility callers materialize the file as bytes.
func recordingBytes(b Build, e recordedEvidence, err error) (Build, Evidence, error) {
	if e.recording != nil {
		if err == nil {
			e.Traces, err = os.ReadFile(e.recording.path)
		}
		err = errors.Join(err, e.recording.close())
	}
	return b, e.Evidence, errors.Join(err, e.replay.close())
}

type recordingWriter struct {
	ctx context.Context
	out io.Writer
	err error
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	if w.err == nil {
		w.err = w.ctx.Err()
	}
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.out.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = errors.Join(err, w.ctx.Err())
	return n, w.err
}

func (w *recordingWriter) text(s string) { _, _ = w.Write([]byte(s)) }

func (w *recordingWriter) value(v any) {
	if w.err != nil {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		w.err = err
		return
	}
	_, _ = w.Write(b)
}
