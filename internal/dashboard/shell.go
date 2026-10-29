package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// shell reads a bounded, literal command vocabulary. It never runs a shell,
// expands a word, or keeps command text in the public view. Files are touched
// only after the whole invocation has one supported read or search kind.
func (t *transcript) shell(command string) string {
	kind, paths := shellCommand(command, t.cwd, t.cwd, 0)
	if kind != "read" && kind != "search" {
		return kind
	}
	seen := map[string]bool{}
	for _, path := range paths {
		rel, ok := shellLocalPath(t.cwd, path, false)
		if ok && !seen[rel] {
			seen[rel] = true
			t.touch(rel, "read")
		}
	}
	return kind
}

// shellLocalPath checks both the spelling and the resolved destination. In
// particular an in-copy symlink cannot publish a path or size outside it.
func shellLocalPath(root, path string, directory bool) (string, bool) {
	if root == "" || !filepath.IsAbs(root) {
		return "", false
	}
	if _, ok := inside(root, path); !ok {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	rel, ok := inside(resolvedRoot, resolved)
	if !ok {
		return "", false
	}
	info, err := os.Stat(resolved)
	if err != nil || (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return "", false
	}
	return rel, true
}

func shellPath(base, path string) string {
	if filepath.IsAbs(path) || base == "" {
		return path
	}
	// Do not clean away a symlink/.. before resolving the actual path.
	return base + string(filepath.Separator) + path
}

type shellPart struct {
	words       []string
	assignments int
	next        string
}

// shellParts accepts literal quotes and escapes, plus simple compound
// separators. Expansion, redirection and other shell grammar are opaque.
func shellParts(command string) ([]shellPart, bool) {
	if len(command) > 64<<10 {
		return nil, false
	}
	var parts []shellPart
	var words []string
	var word strings.Builder
	started, quote := false, byte(0)
	assignments := 0
	nameLiteral, assignmentValue := true, false
	flush := func() {
		if started {
			value := word.String()
			if assignments == len(words) && nameLiteral && shellAssignment(value) {
				assignments++
			}
			words = append(words, value)
			word.Reset()
			started = false
			nameLiteral, assignmentValue = true, false
		}
	}
	for i := 0; i < len(command); i++ {
		c := command[i]
		if c == 0 || c == '\r' {
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
			if i+1 == len(command) {
				return nil, false
			}
			next := command[i+1]
			if quote == '"' && !strings.ContainsRune("$`\"\\\n", rune(next)) {
				word.WriteByte(c)
				continue
			}
			i++
			if next != '\n' {
				if !assignmentValue {
					nameLiteral = false
				}
				word.WriteByte(next)
				started = true
			}
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
			if !assignmentValue {
				nameLiteral = false
			}
			quote, started = c, true
		case ' ', '\t':
			flush()
		case ';', '\n', '|', '&':
			flush()
			op := string(c)
			if (c == '|' || c == '&') && i+1 < len(command) && command[i+1] == c {
				i++
				op += string(c)
			}
			if op == "&" || op == "||" {
				return nil, false
			}
			if len(words) == 0 {
				if c == '\n' {
					continue
				}
				return nil, false
			}
			parts = append(parts, shellPart{words: words, assignments: assignments, next: op})
			words = nil
			assignments = 0
		case '<', '>', '(', ')', '{', '}', '*', '?', '[', ']', '~', '#':
			return nil, false
		default:
			if c == '=' {
				assignmentValue = true
			}
			word.WriteByte(c)
			started = true
		}
		if len(words)+len(parts) > 4096 {
			return nil, false
		}
	}
	if quote != 0 {
		return nil, false
	}
	flush()
	if len(words) > 0 {
		parts = append(parts, shellPart{words: words, assignments: assignments})
	} else if len(parts) > 0 && parts[len(parts)-1].next != ";" && parts[len(parts)-1].next != "\n" {
		return nil, false
	}
	return parts, len(parts) > 0
}

func shellAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, c := range name {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func shellExecutable(word string) string {
	if dir := filepath.Dir(word); dir == "/bin" || dir == "/usr/bin" {
		return filepath.Base(word)
	}
	return word
}

func shellCommand(command, root, base string, depth int) (string, []string) {
	if depth > 4 {
		return "other", nil
	}
	parts, ok := shellParts(command)
	if !ok {
		return "other", nil
	}
	kind := ""
	var paths []string
	for i, part := range parts {
		words := part.words[part.assignments:]
		env := false
		if len(words) > 0 && shellExecutable(words[0]) == "env" {
			env = true
			words = words[1:]
			for len(words) > 0 && shellAssignment(words[0]) {
				words = words[1:]
			}
		}
		if len(words) == 0 {
			return "other", nil
		}
		name := shellExecutable(words[0])
		if name == "cd" {
			args := words[1:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if env || words[0] != "cd" || len(args) != 1 || strings.HasPrefix(args[0], "-") || part.next != "&&" || (i > 0 && parts[i-1].next == "|") {
				return "other", nil
			}
			rel, ok := shellLocalPath(root, shellPath(base, args[0]), true)
			if !ok {
				return "other", nil
			}
			base = filepath.Join(root, filepath.FromSlash(rel))
			continue
		}
		var next string
		var files []string
		switch name {
		case "sh", "bash", "zsh":
			if len(words) != 3 || (words[1] != "-c" && words[1] != "-lc" && words[1] != "-cl") {
				return "other", nil
			}
			next, files = shellCommand(words[2], root, base, depth+1)
		default:
			next, files = shellSimple(name, words[1:])
			operands := files
			files = nil
			for _, path := range operands {
				if path != "-" {
					files = append(files, shellPath(base, path))
				}
			}
		}
		if next == "other" || (kind != "" && kind != next) {
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

// A small option table distinguishes operands from values, especially search
// patterns and globs. An unknown option makes the whole command unsupported.
type shellOption byte

const (
	shellFlag shellOption = iota
	shellValue
	shellPattern
)

func shellOptions(flags, values, patterns string) map[string]shellOption {
	out := map[string]shellOption{}
	for kind, names := range []string{flags, values, patterns} {
		for _, name := range strings.Fields(names) {
			out[name] = shellOption(kind)
		}
	}
	return out
}

var (
	shellCatOptions  = shellOptions("-A -b -e -E -n -s -t -T -u -v --show-all --number-nonblank --show-ends --number --squeeze-blank --show-tabs --show-nonprinting", "", "")
	shellEndOptions  = shellOptions("-q -v -z -f -F --quiet --silent --verbose --zero-terminated --follow --retry", "-n -c --lines --bytes --sleep-interval --pid", "")
	shellSedOptions  = shellOptions("-n --quiet --silent", "", "-e --expression")
	shellPrintScript = regexp.MustCompile(`^(?:(?:[0-9]+|\$)(?:,(?:[0-9]+|\$))?)?p$`)
	shellRGOptions   = shellOptions(
		"-n -N -i -s -S -w -x -v -F -l -L -c -o -q -U -a -z -0 -H -I --files --line-number --no-line-number --ignore-case --case-sensitive --smart-case --word-regexp --line-regexp --invert-match --fixed-strings --files-with-matches --files-without-match --count --count-matches --only-matching --quiet --hidden --no-ignore --no-ignore-vcs --follow --multiline --multiline-dotall --text --null --null-data --no-heading --heading --with-filename --no-filename --stats --pcre2 --crlf --no-messages --column --byte-offset",
		"-g -t -T -A -B -C -m -j -r --glob --iglob --type --type-not --encoding --color --colors --context --after-context --before-context --max-count --max-depth --max-filesize --threads --sort --sortr --replace --field-match-separator --field-context-separator --path-separator --engine",
		"-e -f --regexp --file")
	shellGrepOptions = shellOptions(
		"-n -i -v -E -F -G -P -w -x -c -l -L -o -q -s -a -b -h -H -I -r -R -Z -z --line-number --ignore-case --invert-match --extended-regexp --fixed-strings --basic-regexp --perl-regexp --word-regexp --line-regexp --count --files-with-matches --files-without-match --only-matching --quiet --silent --no-messages --text --byte-offset --with-filename --no-filename --recursive --dereference-recursive --null --null-data",
		"-A -B -C -m -d -D --after-context --before-context --context --max-count --directories --devices --include --exclude --exclude-dir --exclude-from --label --binary-files --color --colour --group-separator",
		"-e -f --regexp --file")
)

type shellArguments struct {
	operands []string
	patterns []string
	list     bool
}

func shellArgs(args []string, options map[string]shellOption) (shellArguments, bool) {
	var out shellArguments
	end := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !end && arg == "--" {
			end = true
			continue
		}
		if end || arg == "-" || !strings.HasPrefix(arg, "-") {
			out.operands = append(out.operands, arg)
			continue
		}
		long := strings.HasPrefix(arg, "--")
		for j := 1; j < len(arg); j++ {
			name, value, attached := "-"+string(arg[j]), "", false
			if long {
				name, value, attached = strings.Cut(arg, "=")
			}
			kind, ok := options[name]
			if !ok || (kind == shellFlag && attached) {
				return shellArguments{}, false
			}
			out.list = out.list || name == "--files"
			if kind != shellFlag {
				if !long && j+1 < len(arg) {
					value, attached = arg[j+1:], true
				}
				if !attached {
					i++
					if i == len(args) {
						return shellArguments{}, false
					}
					value = args[i]
				}
				if kind == shellPattern {
					out.patterns = append(out.patterns, value)
				}
				break
			}
			if long {
				break
			}
		}
	}
	return out, true
}

func shellSimple(name string, args []string) (string, []string) {
	switch name {
	case "cat", "head", "tail", "sed":
		options := shellCatOptions
		if name == "head" || name == "tail" {
			options = shellEndOptions
		} else if name == "sed" {
			options = shellSedOptions
		}
		parsed, ok := shellArgs(args, options)
		if !ok {
			return "other", nil
		}
		if name == "sed" {
			if len(parsed.patterns) == 0 && len(parsed.operands) > 0 {
				parsed.patterns, parsed.operands = parsed.operands[:1], parsed.operands[1:]
			}
			if len(parsed.patterns) == 0 {
				return "other", nil
			}
			for _, script := range parsed.patterns {
				if !shellPrintScript.MatchString(script) {
					return "other", nil
				}
			}
		}
		return "read", parsed.operands
	case "rg", "grep":
		options := shellRGOptions
		if name == "grep" {
			options = shellGrepOptions
		}
		parsed, ok := shellArgs(args, options)
		if !ok {
			return "other", nil
		}
		if parsed.list {
			return "list", nil
		}
		if len(parsed.patterns) == 0 {
			if len(parsed.operands) == 0 {
				return "other", nil
			}
			parsed.operands = parsed.operands[1:]
		}
		return "search", parsed.operands
	case "ls", "pwd":
		return "list", nil
	case "find":
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-type", "-name", "-iname", "-path", "-ipath", "-maxdepth", "-mindepth", "-mtime", "-mmin", "-size", "-user", "-group", "-perm":
				i++
				if i == len(args) {
					return "other", nil
				}
			case "-print", "-print0", "-ls", "-empty", "-depth", "-xdev", "-a", "-o", "-not":
			default:
				if strings.HasPrefix(args[i], "-") {
					return "other", nil
				}
			}
		}
		return "list", nil
	case "go", "cargo":
		if len(args) > 0 && (args[0] == "build" || args[0] == "test") {
			return args[0], nil
		}
	case "make":
		if len(args) == 0 || (len(args) == 1 && args[0] == "all") {
			return "build", nil
		}
	case "npm":
		if len(args) > 0 && args[0] == "test" {
			return "test", nil
		}
		if len(args) > 1 && args[0] == "run" && (args[1] == "build" || args[1] == "test") {
			return args[1], nil
		}
	case "pytest":
		return "test", nil
	case "python", "python3":
		if len(args) >= 2 && args[0] == "-m" && args[1] == "pytest" {
			return "test", nil
		}
	case "git":
		return "git", nil
	}
	return "other", nil
}
