package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// componentPythonDeclaration is one declaration the syntax parser found.
type componentPythonDeclaration struct {
	Symbol    string             `json:"symbol"`
	Contracts [][2]int           `json:"contracts"`
	Exsures   []componentExsures `json:"exsures"`
}

// componentPythonSyntaxKey names one reading: a source's bytes, and the
// interpreter that read them, by its resolved path, size and modification
// time.
type componentPythonSyntaxKey struct {
	interpreter string
	size        int64
	modified    int64
	source      [sha256.Size]byte
}

// componentPythonSyntaxLimit bounds what a long-lived process keeps. Past
// it everything kept is dropped, and the sources still in use are read
// again.
const componentPythonSyntaxLimit = 4096

var componentPythonSyntax = struct {
	sync.Mutex
	kept map[componentPythonSyntaxKey][]byte
}{kept: map[componentPythonSyntaxKey][]byte{}}

// componentPythonDeclarations returns the syntax parser's answer for one
// source.
//
// The parser reads only the bytes it's given, in an isolated interpreter,
// so its answer is a function of those bytes and of that interpreter, and
// one answer serves every later reading while this process lives (#893).
// A gate run resolves an application's inventory about six times, and each
// resolution started an interpreter for every source: one test of the gate
// started the parser 560 times for 11 different sources.
//
// Only an answer that decoded is kept, so a source whose reading failed is
// read again. Another python3 on PATH, or the same one replaced, has a
// different key and is asked again. Each caller decodes its own copy.
func componentPythonDeclarations(src []byte) ([]componentPythonDeclaration, error) {
	key, keyed := componentPythonSyntaxKey{}, false
	interpreter, err := exec.LookPath("python3")
	if err == nil {
		if info, err := os.Stat(interpreter); err == nil {
			key = componentPythonSyntaxKey{interpreter: interpreter, size: info.Size(), modified: info.ModTime().UnixNano(), source: sha256.Sum256(src)}
			keyed = true
		}
	}
	if keyed {
		componentPythonSyntax.Lock()
		b, ok := componentPythonSyntax.kept[key]
		componentPythonSyntax.Unlock()
		if ok {
			var out []componentPythonDeclaration
			if err := json.Unmarshal(b, &out); err == nil {
				return out, nil
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Isolated mode ignores PYTHON* and excludes the project from sys.path.
	// -S also excludes site initialization, including installed startup hooks.
	cmd := exec.CommandContext(ctx, "python3", "-I", "-S", "-c", componentPythonParser)
	if keyed {
		// The interpreter that answers is the one the key names.
		cmd.Path = interpreter
	}
	cmd.Stdin = bytes.NewReader(src)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("isolated Python 3 syntax parser: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []componentPythonDeclaration
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("Python syntax parser output: %w", err)
	}
	if keyed {
		componentPythonSyntax.Lock()
		if len(componentPythonSyntax.kept) >= componentPythonSyntaxLimit {
			clear(componentPythonSyntax.kept)
		}
		componentPythonSyntax.kept[key] = b
		componentPythonSyntax.Unlock()
	}
	return out, nil
}
