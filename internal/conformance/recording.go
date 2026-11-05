package conformance

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type recorded struct {
	store          *recordingStore
	starts         []int
	steps          [][2]int
	attempts       []attemptEntry
	strict         []int
	runs, recorded int
	hasAttempts    bool
}

// A repeated JSON array reuses its old map elements, even after a shorter
// array. Keep those original-position mappings on disk until decoding ends.
type wireSlice struct {
	items  []diskText
	length int
}

type wireRecording struct {
	store    *recordingStore
	states   wireSlice
	traces   []wireSlice
	runs     int
	init     []int
	attempts []diskText
	problem  error
}

// recordingSyntax checks the JSON grammar without accumulating scalar text.
// Decoder.Token has different errors from Decoder.Decode for incomplete
// containers and misplaced commas. Keep Decode's grammar and diagnostics in
// this first pass, before the ordinary decoder reads individual values.
type recordingSyntax struct {
	ctx    context.Context
	reader *bufio.Reader
	offset int64
}

func (s *recordingSyntax) peek() (byte, error) {
	b, err := s.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (s *recordingSyntax) take() (byte, error) {
	c, err := s.reader.ReadByte()
	if err == nil {
		s.offset++
	}
	return c, err
}

func (s *recordingSyntax) space() (byte, error) {
	for {
		c, err := s.peek()
		if err != nil || c != ' ' && c != '\n' && c != '\r' && c != '\t' {
			return c, err
		}
		s.take()
	}
}

func (s *recordingSyntax) invalid(c byte, where string) error {
	prefix := ""
	switch where {
	case "looking for beginning of value":
		prefix = "[null,"
	case "looking for beginning of object key string":
		prefix = "{\"x\":null,"
	case "after object key":
		prefix = "{\"x\""
	case "after object key:value pair":
		prefix = "{\"x\":null"
	case "after array element":
		prefix = "[null"
	case "in numeric literal":
		prefix = "-"
	case "after decimal point in numeric literal":
		prefix = "0."
	case "in exponent of numeric literal":
		prefix = "0e+"
	}
	if prefix != "" {
		return s.syntaxError(prefix)
	}
	quoted := strconv.Quote(string(c))
	quoted = "'" + quoted[1:len(quoted)-1] + "'"
	if c == '\'' {
		quoted = `'\''`
	} else if c == '"' {
		quoted = `'"'`
	}
	return fmt.Errorf("invalid character %s %s", quoted, where)
}

// Ask the ordinary decoder for the diagnostic at this grammar position.
// Eight following bytes cover an escape or a UTF-8 rune, without retaining
// the preceding recording or depending on the runtime's error wording.
func (s *recordingSyntax) syntaxError(prefix string) error {
	tail, _ := s.reader.Peek(8)
	if err := decodeValue([]byte(prefix+string(tail)), new(any)); err != nil {
		return err
	}
	return fmt.Errorf("invalid JSON syntax")
}

func (s *recordingSyntax) scan() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if _, err := s.space(); err != nil {
		return err
	}
	err := s.value(0)
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (s *recordingSyntax) value(depth int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	c, err := s.space()
	if err != nil {
		return err
	}
	switch c {
	case '{', '[':
		if depth == 10000 {
			return s.syntaxError(strings.Repeat("[", 10000))
		}
		return s.compound(c, depth+1)
	case '"':
		return s.quoted()
	case 't', 'f', 'n':
		literal := "true"
		if c == 'f' {
			literal = "false"
		} else if c == 'n' {
			literal = "null"
		}
		for i := range literal {
			got, err := s.take()
			if err != nil {
				return err
			}
			if got != literal[i] {
				return s.syntaxError(literal[:i] + string(got))
			}
		}
		return nil
	default:
		if c == '-' || c >= '0' && c <= '9' {
			return s.number()
		}
		return s.invalid(c, "looking for beginning of value")
	}
}

func (s *recordingSyntax) compound(open byte, depth int) error {
	s.take()
	close, after := byte(']'), "after array element"
	if open == '{' {
		close, after = '}', "after object key:value pair"
	}
	c, err := s.space()
	if err != nil {
		return err
	}
	if c == close {
		s.take()
		return nil
	}
	for {
		if open == '{' {
			c, err = s.space()
			if err != nil {
				return err
			}
			if c != '"' {
				return s.invalid(c, "looking for beginning of object key string")
			}
			if err := s.quoted(); err != nil {
				return err
			}
			c, err = s.space()
			if err != nil {
				return err
			}
			if c != ':' {
				return s.invalid(c, "after object key")
			}
			s.take()
		}
		if err := s.value(depth); err != nil {
			return err
		}
		c, err = s.space()
		if err != nil {
			return err
		}
		if c == close {
			s.take()
			return nil
		}
		if c != ',' {
			return s.invalid(c, after)
		}
		s.take()
	}
}

func (s *recordingSyntax) quoted() error {
	s.take()
	for {
		c, err := s.take()
		if err != nil {
			return err
		}
		if c == '"' {
			return nil
		}
		if c < 0x20 {
			return s.syntaxError("\"" + string(c))
		}
		if c != '\\' {
			continue
		}
		c, err = s.take()
		if err != nil {
			return err
		}
		switch c {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		case 'u':
			escape := [6]byte{'\\', 'u'}
			for i := 0; i < 4; i++ {
				c, err = s.take()
				if err != nil {
					return err
				}
				escape[i+2] = c
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
					return s.syntaxError("\"" + string(escape[:i+3]))
				}
			}
		default:
			return s.syntaxError("\"\\" + string(c))
		}
	}
}

func (s *recordingSyntax) digits() error {
	for {
		c, err := s.peek()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if c < '0' || c > '9' {
			return nil
		}
		s.take()
	}
}

func (s *recordingSyntax) number() error {
	c, _ := s.peek()
	if c == '-' {
		s.take()
		var err error
		c, err = s.peek()
		if err != nil {
			return err
		}
	}
	if c < '0' || c > '9' {
		return s.invalid(c, "in numeric literal")
	}
	s.take()
	if c != '0' {
		if err := s.digits(); err != nil {
			return err
		}
	}
	for _, exponent := range []bool{false, true} {
		c, err := s.peek()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if !exponent && c != '.' || exponent && c != 'e' && c != 'E' {
			continue
		}
		s.take()
		c, err = s.peek()
		if err != nil {
			return err
		}
		where := "after decimal point in numeric literal"
		if exponent {
			where = "in exponent of numeric literal"
			if c == '+' || c == '-' {
				s.take()
				c, err = s.peek()
				if err != nil {
					return err
				}
			}
		}
		if c < '0' || c > '9' {
			return s.invalid(c, where)
		}
		if err := s.digits(); err != nil {
			return err
		}
	}
	return nil
}

func decodeValue(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(value)
}

// skipJSON advances over an already validated, ignored value without
// retaining its contents.
func skipJSON(ctx context.Context, d *json.Decoder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	return skipToken(ctx, d, token)
}

func skipToken(ctx context.Context, d *json.Decoder, token json.Token) error {
	bracket, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	for d.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if bracket == '{' {
			if _, err := d.Token(); err != nil {
				return err
			}
		}
		if err := skipJSON(ctx, d); err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
	_, err := d.Token()
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func decodeReader(ctx context.Context, s *recordingStore, input io.Reader, vars []string) (rec recorded, problem string, err error) {
	rec.store = s
	f, err := os.OpenFile(filepath.Join(s.dir, "input"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return rec, "", storageError(err)
	}
	defer func() { err = errors.Join(err, f.Close(), os.Remove(f.Name())) }()
	reader := &contextReader{ctx: ctx, r: io.TeeReader(input, f)}
	syntax := recordingSyntax{ctx: ctx, reader: bufio.NewReaderSize(reader, 64<<10)}
	if e := syntax.scan(); e != nil {
		if reader.err != nil {
			return rec, "", reader.err
		}
		return rec, "the driver's output isn't valid JSON: " + e.Error(), nil
	}
	if reader.err != nil {
		return rec, "", reader.err
	}
	// A separate scratch store holds decoded maps for repeated-field reuse.
	// No raw JSON or refusal descriptors survive into the checking phases.
	scratch, e := newRecordingStore(ctx)
	if e != nil {
		return rec, "", storageError(e)
	}
	defer func() { err = errors.Join(err, scratch.close()) }()
	wire := wireRecording{store: scratch}
	fields := json.NewDecoder(&contextReader{ctx: ctx, r: io.NewSectionReader(f, 0, syntax.offset)})
	fields.UseNumber()
	if e := wire.read(fields); e != nil {
		return rec, "", e
	}
	if wire.problem != nil {
		return rec, "the driver's output isn't valid JSON: " + wire.problem.Error(), nil
	}
	if e := syntax.scan(); e != io.EOF {
		if reader.err != nil {
			return rec, "", reader.err
		}
		return rec, "the driver's output isn't valid JSON: trailing data", nil
	}
	if reader.err != nil {
		return rec, "", reader.err
	}
	return wire.record(ctx, s, vars)
}

func (w *wireRecording) saveError(err error, field string) {
	if err == nil || w.problem != nil {
		return
	}
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		typ.Field = field
	}
	w.problem = err
}

func (w *wireRecording) array(d *json.Decoder, typ reflect.Type, field string) (bool, bool, error) {
	if err := w.store.ctx.Err(); err != nil {
		return false, false, err
	}
	token, err := d.Token()
	if err != nil {
		return false, false, err
	}
	if token == nil {
		return false, true, nil
	}
	if token == json.Delim('[') {
		return true, false, nil
	}
	value := ""
	switch token.(type) {
	case json.Number:
		value = "number"
	case string:
		value = "string"
	case bool:
		value = "bool"
	case json.Delim:
		value = "object"
	}
	w.saveError(&json.UnmarshalTypeError{Value: value, Type: typ}, field)
	return false, false, skipToken(w.store.ctx, d, token)
}

func (w *wireRecording) read(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	if token != json.Delim('{') {
		// This exceptional case is a single invalid root, not a recording.
		w.problem = &json.UnmarshalTypeError{Value: jsonKind(token), Type: reflect.TypeOf(struct {
			Traces [][]map[string]any `json:"traces"`
			attempts
		}{})}
		return nil
	}
	for d.More() {
		if err := w.store.ctx.Err(); err != nil {
			return err
		}
		key, err := d.Token()
		if err != nil {
			return err
		}
		field := key.(string)
		switch {
		case strings.EqualFold(field, "states"):
			err = w.readStates(d, &w.states, field)
		case strings.EqualFold(field, "traces"):
			var array, null bool
			array, null, err = w.array(d, reflect.TypeOf([][]map[string]any{}), field)
			if null {
				w.traces = nil
				w.runs = 0
			}
			if array {
				i := 0
				for d.More() && err == nil {
					if i == len(w.traces) {
						w.traces = append(w.traces, wireSlice{})
					}
					err = w.readStates(d, &w.traces[i], field+"."+strconv.Itoa(i))
					i++
				}
				w.runs = i
				if i == 0 {
					w.traces = nil
				}
				if err == nil {
					_, err = d.Token()
				}
			}
		case strings.EqualFold(field, "init"):
			var array, null bool
			array, null, err = w.array(d, reflect.TypeOf([]int{}), field)
			if null {
				w.init = nil
			}
			if array {
				i := 0
				for d.More() && err == nil {
					if err = w.store.ctx.Err(); err != nil {
						break
					}
					var raw json.RawMessage
					if err = d.Decode(&raw); err != nil {
						break
					}
					if i >= len(w.init) {
						w.init = w.init[:cap(w.init)]
						if i >= len(w.init) {
							w.init = append(w.init, 0)
						}
					}
					w.saveError(decodeValue(raw, &w.init[i]), field+"."+strconv.Itoa(i))
					i++
				}
				w.init = w.init[:i]
				if i == 0 {
					w.init = nil
				}
				if err == nil {
					_, err = d.Token()
				}
			}
		case strings.EqualFold(field, "attempts"):
			var array, null bool
			array, null, err = w.array(d, reflect.TypeOf([]attemptFields{}), field)
			if null {
				w.attempts = nil
			}
			if array {
				w.attempts = []diskText{}
				for d.More() && err == nil {
					if err = w.store.ctx.Err(); err != nil {
						break
					}
					var raw json.RawMessage
					if err = d.Decode(&raw); err != nil {
						break
					}
					var a attemptFields
					if e := a.UnmarshalJSON(raw); e != nil {
						// A custom unmarshaler's failure supersedes any saved
						// type error and ends decoding this valid JSON value.
						w.problem = e
						return nil
					}
					var ref diskText
					ref, err = w.store.put(string(raw))
					w.attempts = append(w.attempts, ref)
				}
				if err == nil {
					_, err = d.Token()
				}
			}
		default:
			err = skipJSON(w.store.ctx, d)
		}
		if err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func jsonKind(token json.Token) string {
	switch token.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case json.Number:
		return "number"
	}
	return "array"
}

func (w *wireRecording) readStates(d *json.Decoder, states *wireSlice, field string) error {
	array, null, err := w.array(d, reflect.TypeOf([]map[string]any{}), field)
	if null {
		*states = wireSlice{}
	}
	if !array || err != nil {
		return err
	}
	i := 0
	for d.More() {
		if err := w.store.ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return err
		}
		var state map[string]any
		if i < len(states.items) {
			old, err := w.store.read(states.items[i])
			if err != nil {
				return err
			}
			if err := decodeValue([]byte(old), &state); err != nil {
				return err
			}
		}
		w.saveError(decodeValue(raw, &state), field+"."+strconv.Itoa(i))
		encoded, err := json.Marshal(state)
		if err != nil {
			return err
		}
		ref, err := w.store.put(string(encoded))
		if err != nil {
			return err
		}
		if i == len(states.items) {
			states.items = append(states.items, ref)
		} else {
			states.items[i] = ref
		}
		i++
	}
	states.length = i
	if i == 0 {
		states.items = nil
	}
	_, err = d.Token()
	return err
}

func (w *wireRecording) record(ctx context.Context, s *recordingStore, vars []string) (rec recorded, problem string, err error) {
	rec.store = s
	starts, steps := map[int]bool{}, map[[2]int]bool{}
	add := func(ref diskText) (int, string, error) {
		raw, err := w.store.read(ref)
		if err != nil {
			return 0, "", err
		}
		var state map[string]any
		if err := decodeValue([]byte(raw), &state); err != nil {
			return 0, "", err
		}
		text, err := encodeRecord(state, vars)
		if err != nil {
			return 0, fmt.Sprintf("a recorded state can't be read: %v", err), nil
		}
		i, err := s.state(text)
		return i, "", err
	}
	if w.attempts == nil {
		rec.runs = w.runs
		if rec.runs == 0 {
			return rec, "the driver recorded no runs", nil
		}
		for _, run := range w.traces[:w.runs] {
			prev := -1
			for n, ref := range run.items[:run.length] {
				i, p, e := add(ref)
				if e != nil || p != "" {
					return rec, p, e
				}
				if n == 0 {
					starts[i] = true
				} else {
					rec.recorded++
					if i != prev {
						steps[[2]int{prev, i}] = true
					}
				}
				prev = i
			}
		}
	} else {
		rec.hasAttempts = true
		ids := make([]int, w.states.length)
		for n, ref := range w.states.items[:w.states.length] {
			i, p, e := add(ref)
			if e != nil || p != "" {
				return rec, p, e
			}
			ids[n] = i
		}
		for _, n := range w.init {
			if err := ctx.Err(); err != nil {
				return rec, "", err
			}
			if n < 0 || n >= len(ids) {
				return rec, fmt.Sprintf("the driver starts in state %d, which it didn't record", n), nil
			}
			starts[ids[n]] = true
		}
		if len(starts) == 0 {
			return rec, "the driver recorded no state to start in", nil
		}
		state := func(v any) (int, bool) {
			f, ok := v.(json.Number)
			n, e := f.Int64()
			if !ok || e != nil || n < 0 || n >= int64(len(ids)) {
				return 0, false
			}
			return ids[n], true
		}
		for _, ref := range w.attempts {
			raw, e := w.store.read(ref)
			if e != nil {
				return rec, "", e
			}
			var a []any
			if e := decodeValue([]byte(raw), &a); e != nil {
				return rec, "", e
			}
			if len(a) != 4 && len(a) != 5 {
				return rec, fmt.Sprintf("an attempt is %v; want [from, step, [args], to] with optional declared outcome metadata", a), nil
			}
			from, okFrom := state(a[0])
			name, okName := a[1].(string)
			args, okArgs := a[2].([]any)
			to, okTo := state(a[3])
			if !okFrom || !okName || !okArgs || !okTo {
				return rec, fmt.Sprintf("an attempt is %v; want [from, step, [args], to], with states the driver recorded", a), nil
			}
			t := attempt{from: from, to: to, step: name}
			for _, arg := range args {
				text, e := Encode(arg)
				if e != nil {
					return rec, fmt.Sprintf("an argument of %s can't be read: %v", name, e), nil
				}
				t.args = append(t.args, text)
			}
			if len(a) == 5 {
				t.outcome, problem = declaredOutcome(a[4])
				if problem != "" || t.outcome == "refused" && from != to {
					pretty, e := s.pretty(from)
					if e != nil {
						return rec, "", e
					}
					if problem != "" {
						return rec, fmt.Sprintf("attempt %s in %s: %s", actionCall(t), pretty, problem), nil
					}
					end, e := s.pretty(to)
					if e != nil {
						return rec, "", e
					}
					return rec, fmt.Sprintf("refused attempt %s changed state from %s to %s", actionCall(t), pretty, end), nil
				}
			}
			encoded, e := json.Marshal(attemptText{Step: t.step, Args: t.args, Outcome: t.outcome})
			if e != nil {
				return rec, "", e
			}
			text, e := s.put(string(encoded))
			if e != nil {
				return rec, "", e
			}
			if t.outcome != "" {
				rec.strict = append(rec.strict, len(rec.attempts))
			}
			rec.attempts = append(rec.attempts, attemptEntry{from: from, to: to, text: text})
			rec.recorded++
			if from != to {
				steps[[2]int{from, to}] = true
			}
		}
	}
	for id := range starts {
		if e := ctx.Err(); e != nil {
			return rec, "", e
		}
		rec.starts = append(rec.starts, id)
	}
	if err := sortEntries(ctx, rec.starts, func(a, b int) (int, error) { return s.compare(s.states[a].text, s.states[b].text) }); err != nil {
		return rec, "", err
	}
	for pair := range steps {
		if e := ctx.Err(); e != nil {
			return rec, "", e
		}
		rec.steps = append(rec.steps, pair)
	}
	if err := sortEntries(ctx, rec.steps, pairOrder); err != nil {
		return rec, "", err
	}
	s.index = nil
	return rec, "", nil
}
