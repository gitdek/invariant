package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// shell classifies a whole invocation before publishing any operands. It
// never runs a command, expands a word, or reads a file's contents.
func (t *transcript) shell(command string) string {
	kind, paths := shellInvocation(command, t.cwd, t.cwd, 0)
	seen := map[string]bool{}
	for _, p := range paths {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		rel, ok := inside(t.cwd, real)
		if !ok || seen[rel] {
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		seen[rel] = true
		t.touch(real, "read")
	}
	return kind
}

type shellPart struct {
	args []string
	next string
}

// shellWords accepts literal words, quoting, && and pipelines. Everything
// requiring expansion or other shell syntax makes the invocation opaque.
func shellWords(command string) ([]shellPart, bool) {
	var parts []shellPart
	var args []string
	var word strings.Builder
	started := false
	var quote byte
	flush := func() {
		if started {
			args = append(args, word.String())
			word.Reset()
			started = false
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == 0 || c == '\n' || c == '\r' {
			return nil, false
		}
		if quote == '\'' {
			if c == quote {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		if c == '\\' {
			if i+1 == len(command) || command[i+1] == '\n' || command[i+1] == '\r' || command[i+1] == 0 {
				return nil, false
			}
			if quote == '"' && !strings.ContainsRune("$`\"\\", rune(command[i+1])) {
				word.WriteByte(c)
			} else {
				i++
				word.WriteByte(command[i])
			}
			started = true
			continue
		}
		if c == '$' || c == '`' {
			return nil, false
		}
		if quote == '"' {
			if c == quote {
				quote = 0
			} else {
				word.WriteByte(c)
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote, started = c, true
		case ' ', '\t':
			flush()
		case '&', '|':
			flush()
			op := string(c)
			if c == '&' {
				if i+1 == len(command) || command[i+1] != '&' {
					return nil, false
				}
				op = "&&"
				i++
			} else if i+1 < len(command) && command[i+1] == '|' {
				return nil, false
			}
			if len(args) == 0 {
				return nil, false
			}
			parts = append(parts, shellPart{args: args, next: op})
			args = nil
		case ';', '<', '>', '(', ')', '{', '}', '*', '?', '[', ']', '~', '#', '!':
			return nil, false
		default:
			word.WriteByte(c)
			started = true
		}
	}
	flush()
	if quote != 0 || len(args) == 0 {
		return nil, false
	}
	return append(parts, shellPart{args: args}), true
}

// systemShells are the shells at their system paths, where they are the
// shells they say.
var systemShells = map[string]bool{"/bin/sh": true, "/bin/bash": true, "/bin/zsh": true}

func shellInvocation(command, root, cwd string, depth int) (string, []string) {
	if depth > 8 {
		return "other", nil
	}
	parts, ok := shellWords(command)
	if !ok {
		return "other", nil
	}
	kind := ""
	var paths []string
	for i, part := range parts {
		args := part.args
		// Codex runs every command in a system shell named by its path, as
		// /bin/zsh -lc '…', so the whole command may be one, by name
		// (D-0160). Inside it, a path still names an unknown program.
		if depth == 0 && len(parts) == 1 && systemShells[args[0]] {
			args = append([]string{filepath.Base(args[0])}, args[1:]...)
		}
		// A basename cannot establish which executable a path names.
		// Assignment prefixes and env are deliberately not unwrapped.
		if strings.ContainsAny(args[0], "/=") {
			return "other", nil
		}
		if args[0] == "cd" {
			if len(args) != 2 || strings.HasPrefix(args[1], "-") || part.next != "&&" || (i > 0 && parts[i-1].next == "|") {
				return "other", nil
			}
			var ok bool
			cwd, ok = shellDirectory(root, cwd, args[1])
			if !ok {
				return "other", nil
			}
			continue
		}
		var next string
		var files []string
		switch args[0] {
		case "sh", "bash", "zsh":
			if len(args) != 3 || (args[1] != "-c" && args[1] != "-lc") {
				return "other", nil
			}
			next, files = shellInvocation(args[2], root, cwd, depth+1)
		default:
			next, files = shellSimple(args)
			for j, file := range files {
				files[j] = shellPath(cwd, file)
			}
		}
		if next == "other" || (kind != "" && next != kind) {
			return "other", nil
		}
		kind = next
		paths = append(paths, files...)
	}
	if kind == "" {
		return "other", nil
	}
	return kind, paths
}

// Keep operand traversal intact: cleaning link/../file before resolving
// links could describe a different file from the one the command reads.
func shellPath(cwd, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if cwd == "" || p == "" || p == "-" {
		return ""
	}
	return cwd + string(filepath.Separator) + p
}

// cd is logical in these shells. If physical resolution disagrees with
// the logical path, leave the whole invocation opaque rather than guess.
func shellDirectory(root, cwd, p string) (string, bool) {
	p = shellPath(cwd, p)
	real, err := filepath.EvalSymlinks(p)
	if err != nil || real != filepath.Clean(p) {
		return "", false
	}
	if _, ok := inside(root, real); !ok {
		return "", false
	}
	info, err := os.Stat(real)
	return real, err == nil && info.IsDir()
}

// shellOptions consumes only modeled options, including options after an
// operand and each letter of a short cluster. Values never become files.
func shellOptions(args []string, flags, values string) ([]string, map[string][]string, bool) {
	known := map[string]bool{}
	for _, flag := range strings.Fields(flags) {
		known[flag] = false
	}
	for _, flag := range strings.Fields(values) {
		known[flag] = true
	}
	options := map[string][]string{}
	var operands []string
	ended := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if ended || arg == "-" || !strings.HasPrefix(arg, "-") {
			operands = append(operands, arg)
			continue
		}
		if arg == "--" {
			ended = true
			continue
		}
		name, value, attached := strings.Cut(arg, "=")
		if takes, ok := known[name]; ok {
			if attached && !takes {
				return nil, nil, false
			}
			if takes && !attached {
				i++
				if i == len(args) {
					return nil, nil, false
				}
				value = args[i]
			}
			options[name] = append(options[name], value)
			continue
		}
		if strings.HasPrefix(arg, "--") {
			return nil, nil, false
		}
		for j := 1; j < len(arg); j++ {
			name := "-" + arg[j:j+1]
			takes, ok := known[name]
			if !ok {
				return nil, nil, false
			}
			value := ""
			if takes {
				value = arg[j+1:]
				if value == "" {
					i++
					if i == len(args) {
						return nil, nil, false
					}
					value = args[i]
				}
			}
			options[name] = append(options[name], value)
			if takes {
				break
			}
		}
	}
	return operands, options, true
}

var shellSedPrint = regexp.MustCompile(`^[0-9]+(,[0-9]+)?p$`)

// shellSimple is a bounded vocabulary, not an attempt to infer arbitrary
// programs' behavior. An unmodeled form rejects the whole invocation
// (D-0159).
func shellSimple(args []string) (string, []string) {
	name, args := args[0], args[1:]
	var kind, flags, values string
	switch name {
	case "cat":
		kind, flags = "read", "-n -b -s -E -T -v -A --number --number-nonblank --squeeze-blank"
	case "head", "tail":
		kind, flags, values = "read", "-q -v --quiet --verbose", "-n -c --lines --bytes"
	case "sed":
		files, _, ok := shellOptions(args, "-n", "")
		if !ok || len(files) == 0 || !shellSedPrint.MatchString(files[0]) {
			return "other", nil
		}
		return "read", files[1:]
	case "rg", "grep":
		flags, values = "-n -i -l -c -v -w -F -H", "-e --regexp"
		if name == "rg" {
			flags += " -s --files --hidden"
			values += " -g --glob"
		} else {
			// -h hides file names in grep, where ripgrep prints its help.
			flags += " -E -h"
		}
		files, opts, ok := shellOptions(args, flags, values)
		if !ok {
			return "other", nil
		}
		pattern := len(opts["-e"])+len(opts["--regexp"]) > 0
		if len(opts["--files"]) > 0 {
			if pattern {
				return "other", nil
			}
			return "list", nil
		}
		if !pattern {
			if len(files) == 0 {
				return "other", nil
			}
			files = files[1:]
		}
		return "search", files
	case "ls":
		kind, flags = "list", "-l -a -h -R -d -A -1 --all --almost-all --directory"
	case "pwd":
		if len(args) == 0 {
			return "list", nil
		}
		return "other", nil
	case "find":
		kind, values = "list", "-type -name"
	case "go":
		if len(args) == 0 || (args[0] != "build" && args[0] != "test") {
			return "other", nil
		}
		kind, args = args[0], args[1:]
		flags, values = "-v -race -x -n -trimpath", "-tags -o -p"
		if kind == "test" {
			flags += " -short -cover"
			values += " -run -count -timeout -parallel -bench -benchtime"
		}
	case "make":
		if len(args) == 0 || (len(args) == 1 && args[0] == "all") {
			return "build", nil
		}
		return "other", nil
	case "cargo":
		if len(args) == 0 || (args[0] != "build" && args[0] != "test") {
			return "other", nil
		}
		kind, args = args[0], args[1:]
		flags, values = "--release --quiet -q --verbose -v", "--package -p --features --target"
	case "npm":
		if len(args) == 2 && args[0] == "run" && args[1] == "build" {
			return "build", nil
		}
		if len(args) == 0 || args[0] != "test" {
			return "other", nil
		}
		kind, args, flags = "test", args[1:], "--runInBand"
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
	case "python", "python3":
		if len(args) < 2 || args[0] != "-m" || args[1] != "pytest" {
			return "other", nil
		}
		return shellSimple(append([]string{"pytest"}, args[2:]...))
	case "pytest":
		kind, flags, values = "test", "-q -v -x -s --quiet --verbose", "-k -m --maxfail"
	case "git":
		if len(args) >= 2 && args[0] == "-C" {
			args = args[2:]
		}
		if len(args) == 0 {
			return "other", nil
		}
		switch args[0] {
		case "status":
			flags = "--short -s --branch -b --porcelain"
		case "diff":
			flags, values = "--stat --name-only --name-status --cached --staged --check", "-U"
		default:
			return "other", nil
		}
		kind, args = "git", args[1:]
	default:
		return "other", nil
	}
	files, _, ok := shellOptions(args, flags, values)
	if !ok {
		return "other", nil
	}
	if kind == "read" {
		return kind, files
	}
	return kind, nil
}
