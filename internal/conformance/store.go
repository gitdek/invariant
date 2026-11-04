package conformance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// diskText is a fixed-size index entry. Prefixes accelerate ordering; neither
// prefixes nor hashes stand in for equality of the complete canonical text.
type diskText struct {
	offset, size int64
	prefix       [32]byte
}

type stateEntry struct {
	text diskText
	next int
}

type attemptEntry struct {
	from, to int
	text     diskText
}

type attemptText struct {
	Step    string
	Args    []string
	Outcome string
}

type recordingStore struct {
	ctx    context.Context
	dir    string
	file   *os.File
	end    int64
	states []stateEntry
	index  map[[32]byte]int
}

func newRecordingStore(ctx context.Context) (*recordingStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "invariant-conformance-")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "values"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	return &recordingStore{ctx: ctx, dir: dir, file: f, index: map[[32]byte]int{}}, nil
}

func (s *recordingStore) close() error {
	return errors.Join(s.file.Close(), os.RemoveAll(s.dir))
}

func (s *recordingStore) put(text string) (diskText, error) {
	if err := s.ctx.Err(); err != nil {
		return diskText{}, err
	}
	r := diskText{offset: s.end, size: int64(len(text))}
	copy(r.prefix[:], text)
	n, err := io.WriteString(s.file, text)
	s.end += int64(n)
	if err == nil && n != len(text) {
		err = io.ErrShortWrite
	}
	return r, err
}

func (s *recordingStore) read(r diskText) (string, error) {
	if err := s.ctx.Err(); err != nil {
		return "", err
	}
	b := make([]byte, r.size)
	_, err := s.file.ReadAt(b, r.offset)
	return string(b), err
}

func (s *recordingStore) equal(r diskText, text string) (bool, error) {
	if r.size != int64(len(text)) {
		return false, nil
	}
	var buf [4096]byte
	for off := int64(0); off < r.size; {
		if err := s.ctx.Err(); err != nil {
			return false, err
		}
		n := min(int64(len(buf)), r.size-off)
		if _, err := s.file.ReadAt(buf[:n], r.offset+off); err != nil {
			return false, err
		}
		if !bytes.Equal(buf[:n], []byte(text[off:off+n])) {
			return false, nil
		}
		off += n
	}
	return true, nil
}

func (s *recordingStore) compare(a, b diskText) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if c := bytes.Compare(a.prefix[:min(a.size, 32)], b.prefix[:min(b.size, 32)]); c != 0 {
		return c, nil
	}
	var x, y [4096]byte
	for off := int64(0); off < min(a.size, b.size); {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		n := min(int64(len(x)), min(a.size, b.size)-off)
		if _, err := s.file.ReadAt(x[:n], a.offset+off); err != nil {
			return 0, err
		}
		if _, err := s.file.ReadAt(y[:n], b.offset+off); err != nil {
			return 0, err
		}
		if c := bytes.Compare(x[:n], y[:n]); c != 0 {
			return c, nil
		}
		off += n
	}
	switch {
	case a.size < b.size:
		return -1, nil
	case a.size > b.size:
		return 1, nil
	}
	return 0, nil
}

func (s *recordingStore) state(text string) (int, error) {
	return s.stateHash(text, sha256.Sum256([]byte(text)))
}

func (s *recordingStore) stateHash(text string, hash [32]byte) (int, error) {
	for link := s.index[hash]; link != 0; link = s.states[link-1].next {
		equal, err := s.equal(s.states[link-1].text, text)
		if err != nil {
			return 0, err
		}
		if equal {
			return link - 1, nil
		}
	}
	r, err := s.put(text)
	if err != nil {
		return 0, err
	}
	i := len(s.states)
	s.states = append(s.states, stateEntry{text: r, next: s.index[hash]})
	s.index[hash] = i + 1
	return i, nil
}

func (s *recordingStore) attempt(e attemptEntry) (attempt, error) {
	raw, err := s.read(e.text)
	if err != nil {
		return attempt{}, err
	}
	var a attemptText
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return attempt{}, err
	}
	return attempt{from: e.from, to: e.to, step: a.Step, args: a.Args, outcome: a.Outcome}, nil
}

func (s *recordingStore) pretty(id int) (string, error) {
	text, err := s.read(s.states[id].text)
	if err != nil {
		return "", err
	}
	// Only the outer record's separators change. Nested records, tuples,
	// functions and quoted strings keep their canonical spelling.
	text = text[1 : len(text)-1]
	var out strings.Builder
	depth, quoted, escaped := 0, false, false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
		} else {
			if depth == 0 && strings.HasPrefix(text[i:], " |-> ") {
				out.WriteString(" = ")
				i += 4
				continue
			}
			switch c {
			case '"':
				quoted = true
			case '[', '{', '(':
				depth++
			case ']', '}', ')':
				depth--
			case '<':
				if i+1 < len(text) && text[i+1] == '<' {
					depth++
					out.WriteByte(c)
					i++
				}
			case '>':
				if i+1 < len(text) && text[i+1] == '>' {
					depth--
					out.WriteByte(c)
					i++
				}
			}
		}
		out.WriteByte(c)
	}
	return out.String(), nil
}

// sortEntries can stop between comparisons, including during disk ordering.
// Its scratch holds only the same fixed-size entries as the index it sorts.
func sortEntries[T any](ctx context.Context, entries []T, compare func(T, T) (int, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp := make([]T, len(entries))
	for width := 1; width < len(entries); width *= 2 {
		for lo := 0; lo < len(entries); lo += 2 * width {
			mid, hi := min(lo+width, len(entries)), min(lo+2*width, len(entries))
			i, j := lo, mid
			for k := lo; k < hi; k++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				c := -1
				if i < mid && j < hi {
					var err error
					c, err = compare(entries[i], entries[j])
					if err != nil {
						return err
					}
				}
				if i < mid && (j == hi || c <= 0) {
					tmp[k] = entries[i]
					i++
				} else {
					tmp[k] = entries[j]
					j++
				}
			}
		}
		copy(entries, tmp)
	}
	return ctx.Err()
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
	err error
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		r.err = err
		return 0, err
	}
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	if cause := r.ctx.Err(); cause != nil {
		r.err = cause
		return 0, cause
	}
	return n, err
}

// moduleWriter writes data expressions one value at a time, even when one
// state's tried set is larger than all the other windows put together.
type moduleWriter struct {
	ctx context.Context
	w   *bufio.Writer
	err error
}

func (w *moduleWriter) text(text string) {
	if w.err == nil {
		w.err = w.ctx.Err()
	}
	if w.err == nil {
		_, w.err = w.w.WriteString(text)
	}
}

func (w *moduleWriter) value(s *recordingStore, r diskText) {
	if w.err != nil {
		return
	}
	_, w.err = io.Copy(w.w, &contextReader{ctx: w.ctx, r: io.NewSectionReader(s.file, r.offset, r.size)})
	// SectionReader's EOF also describes a truncated backing file. Check the
	// number copied so a truncated store cannot produce a passing obligation.
	if w.err == nil {
		info, err := s.file.Stat()
		if err != nil {
			w.err = err
		} else if info.Size() < r.offset+r.size {
			w.err = io.ErrUnexpectedEOF
		}
	}
}

func writeModule(ctx context.Context, dir, name string, emit func(*moduleWriter) error) error {
	f, err := os.OpenFile(filepath.Join(dir, name+".tla"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := &moduleWriter{ctx: ctx, w: bufio.NewWriterSize(f, 64<<10)}
	err = emit(w)
	return errors.Join(err, w.err, w.w.Flush(), f.Close(), ctx.Err())
}

func batchDir(ctx context.Context, source, root, name string) (string, error) {
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tla") {
			continue
		}
		if err := copyModule(ctx, filepath.Join(source, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// Atomic creation isolates concurrent invocations while keeping an ordinary
// run's relative paths stable in TLC's operational receipts. Existing paths,
// including caller-owned files and directories, are never reused.
func batchRoot(ctx context.Context, dir string) (string, error) {
	for i := 1; ; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		root := filepath.Join(dir, fmt.Sprintf(".invariant-conformance-%d", i))
		err := os.Mkdir(root, 0700)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return root, nil
	}
}

func copyModule(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, &contextReader{ctx: ctx, r: in})
	return errors.Join(err, out.Close())
}

func batchCount(n, size int) int { return (n + size - 1) / size }

func pairOrder(a, b [2]int) (int, error) {
	for i := range a {
		if a[i] < b[i] {
			return -1, nil
		}
		if a[i] > b[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func storageError(err error) error { return fmt.Errorf("conformance storage: %w", err) }
