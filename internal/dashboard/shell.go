package dashboard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// shellActivity recognizes a deliberately bounded, literal shell vocabulary.
// It never runs a command, expands a word, or reads file contents. A compound
// invocation gets one kind; any unknown or mixed part discards all operands.
func shellActivity(command, workspace string) (string, []string) {
	cwd, root := "", ""
	if workspace != "" {
		cwd, _ = filepath.Abs(workspace)
		root, _ = filepath.EvalSymlinks(cwd)
	}
	kind, operands, ok := shellCommands(command, cwd, root, 0)
	if !ok || kind == "" {
		return "other", nil
	}
	var paths []string
	seen := map[string]bool{}
	for _, operand := range operands {
		// Do not clean before resolving: link/../file follows the link
		// before walking to its parent when used as a file operand.
		p, err := filepath.EvalSymlinks(operand)
		if err != nil || !shellWithin(root, p) || seen[p] {
			continue
		}
		info, err := os.Stat(p)
		if err == nil && info.Mode().IsRegular() {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	return kind, paths
}

func shellWithin(root, p string) bool {
	if root == "" || !filepath.IsAbs(p) {
		return false
	}
	_, ok := inside(root, p)
	return ok
}

type shellWord struct {
	text       string
	op         bool
	assignment bool // an unquoted NAME= prefix, for shell assignments
}

// shellWords retains operators separately from quoted literal punctuation.
// Expansions, redirects, comments, subshells and control syntax are outside
// this vocabulary, even when a supported command appears next to them.
func shellWords(s string) ([]shellWord, bool) {
	if len(s) > 64<<10 {
		return nil, false
	}
	var out []shellWord
	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}
		if s[i] == '|' || s[i] == '&' {
			op := s[i : i+1]
			i++
			if op == "&" {
				if i == len(s) || s[i] != '&' {
					return nil, false
				}
				op, i = "&&", i+1
			} else if i < len(s) && s[i] == '|' {
				return nil, false
			}
			out = append(out, shellWord{text: op, op: true})
			continue
		}
		var word strings.Builder
		assignment, prefix := false, true
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '|' && s[i] != '&' {
			c := s[i]
			switch c {
			case '\'', '"':
				prefix = false
				quote := c
				i++
				for i < len(s) && s[i] != quote {
					c = s[i]
					if c == 0 || c == '\n' || c == '\r' || (quote == '"' && (c == '$' || c == '`')) {
						return nil, false
					}
					if quote == '"' && c == '\\' && i+1 < len(s) && strings.ContainsRune("$`\"\\", rune(s[i+1])) {
						i++
						c = s[i]
					}
					word.WriteByte(c)
					i++
				}
				if i == len(s) {
					return nil, false
				}
				i++
			case '\\':
				prefix = false
				i++
				if i == len(s) || s[i] == '\n' || s[i] == '\r' || s[i] == 0 {
					return nil, false
				}
				word.WriteByte(s[i])
				i++
			default:
				if strings.ContainsRune("\x00\n\r;$`<>*?[]{}()~#", rune(c)) {
					return nil, false
				}
				if c == '=' && prefix {
					assignment = shellName(word.String())
					prefix = false
				}
				word.WriteByte(c)
				i++
			}
		}
		out = append(out, shellWord{text: word.String(), assignment: assignment})
		if len(out) > 4096 {
			return nil, false
		}
	}
	return out, len(out) > 0
}

func shellName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c != '_' && !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func shellCommands(command, cwd, root string, depth int) (string, []string, bool) {
	if depth > 4 {
		return "", nil, false
	}
	words, ok := shellWords(command)
	if !ok {
		return "", nil, false
	}
	var kind string
	var files []string
	for start := 0; start < len(words); {
		end := start
		for end < len(words) && !words[end].op {
			end++
		}
		if start == end || end == len(words)-1 {
			return "", nil, false
		}
		k, operands, next, ok := shellSimple(words[start:end], cwd, root, depth)
		if !ok || (k != "" && kind != "" && k != kind) {
			return "", nil, false
		}
		if next != "" {
			// A pipeline runs cd in a separate shell; this bounded reader
			// accepts directory changes only in && sequences.
			if (start > 0 && words[start-1].text == "|") || (end < len(words) && words[end].text == "|") {
				return "", nil, false
			}
			cwd = next
		}
		if k != "" {
			kind = k
		}
		files = append(files, operands...)
		start = end + 1
	}
	return kind, files, true
}

func shellProgram(s string) string {
	if !strings.ContainsRune(s, filepath.Separator) {
		return s
	}
	switch filepath.Dir(s) {
	case "/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin":
		return filepath.Base(s)
	}
	return ""
}

func shellSimple(words []shellWord, cwd, root string, depth int) (kind string, files []string, next string, ok bool) {
	for len(words) > 0 && words[0].assignment {
		if strings.HasPrefix(words[0].text, "CDPATH=") {
			return
		}
		words = words[1:]
	}
	if len(words) == 0 {
		return
	}
	program := shellProgram(words[0].text)
	if program == "env" {
		words = words[1:]
		if len(words) > 0 && words[0].text == "--" {
			words = words[1:]
		}
		for len(words) > 0 {
			name, _, found := strings.Cut(words[0].text, "=")
			if !found || !shellName(name) {
				break
			}
			if name == "CDPATH" {
				return
			}
			words = words[1:]
		}
		if len(words) == 0 || shellProgram(words[0].text) == "cd" {
			return
		}
		program = shellProgram(words[0].text)
	}
	args := make([]string, 0, len(words)-1)
	for _, word := range words[1:] {
		args = append(args, word.text)
	}
	if program == "sh" || program == "bash" || program == "zsh" {
		if len(args) != 2 || (args[0] != "-c" && args[0] != "-lc") {
			return
		}
		kind, files, ok = shellCommands(args[1], cwd, root, depth+1)
		return
	}
	if program == "cd" {
		if len(args) == 2 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") || cwd == "" {
			return
		}
		next = args[0]
		if !filepath.IsAbs(next) {
			next = filepath.Join(cwd, next)
		} else {
			next = filepath.Clean(next)
		}
		resolved, err := filepath.EvalSymlinks(next)
		if err != nil || !shellWithin(root, resolved) {
			return "", nil, "", false
		}
		info, err := os.Stat(resolved)
		return "", nil, next, err == nil && info.IsDir()
	}
	var operands []string
	kind, operands, ok = shellCommand(program, args)
	if !ok || cwd == "" {
		return
	}
	for _, p := range operands {
		if p == "" || p == "-" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = cwd + string(filepath.Separator) + p
		}
		files = append(files, p)
	}
	return
}

// shellOptions returns positional operands and option arguments separately.
// Unknown options invalidate the command instead of being mistaken for files.
func shellOptions(args []string, flags, values string) ([]string, map[string][]string, bool) {
	allowed := func(set, option string) bool { return strings.Contains(" "+set+" ", " "+option+" ") }
	var positional []string
	options := map[string][]string{}
	ended := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if ended || arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}
		if arg == "--" {
			ended = true
			continue
		}
		parts := []string{arg}
		name, _, _ := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") && len(arg) > 2 && !allowed(flags, name) && !allowed(values, name) {
			parts = nil
			for j := 1; j < len(arg); j++ {
				option := "-" + arg[j:j+1]
				parts = append(parts, option)
				if allowed(values, option) && j+1 < len(arg) {
					parts[len(parts)-1] += "=" + arg[j+1:]
					break
				}
			}
		}
		for _, part := range parts {
			option, value, attached := strings.Cut(part, "=")
			switch {
			case allowed(flags, option) && !attached:
				options[option] = append(options[option], "")
			case allowed(values, option):
				if !attached {
					i++
					if i >= len(args) {
						return nil, nil, false
					}
					value = args[i]
				}
				options[option] = append(options[option], value)
			default:
				return nil, nil, false
			}
		}
	}
	return positional, options, true
}

var shellSedPrint = regexp.MustCompile(`^(?:[0-9]+|\$)?(?:,(?:[0-9]+|\$))?p$`)

func shellCommand(program string, args []string) (string, []string, bool) {
	switch program {
	case "cat":
		files, _, ok := shellOptions(args, "-A -b -e -E -n -s -t -T -u -v --show-all --number-nonblank --show-ends --number --squeeze-blank --show-tabs --show-nonprinting", "")
		return "read", files, ok
	case "head", "tail":
		files, _, ok := shellOptions(args, "-q -v --quiet --silent --verbose", "-n -c --lines --bytes")
		return "read", files, ok
	case "sed":
		files, opts, ok := shellOptions(args, "-n --quiet --silent", "-e --expression")
		scripts := append(opts["-e"], opts["--expression"]...)
		if len(scripts) == 0 && len(files) > 0 {
			scripts, files = files[:1], files[1:]
		}
		if !ok || len(scripts) == 0 {
			return "", nil, false
		}
		for _, script := range scripts {
			if !shellSedPrint.MatchString(script) {
				return "", nil, false
			}
		}
		return "read", files, true
	case "rg", "grep":
		// Their short options are not interchangeable: rg -r and -E take
		// replacement and encoding values, while grep uses both as flags.
		// Keep each grammar explicit so no value can become a file operand.
		var flags, values string
		if program == "rg" {
			flags = "-n -i -v -w -x -l -L -c -o -q -s -H -a -I -F -P -S -U -z -0 --line-number --ignore-case --invert-match --word-regexp --line-regexp --files-with-matches --files-without-match --count --only-matching --quiet --no-messages --no-filename --with-filename --text --fixed-strings --case-sensitive --pcre2 --files --hidden --no-ignore --no-ignore-vcs --follow --no-heading --heading --column --stats --smart-case --multiline --crlf --null --search-zip"
			values = "-e -f -m -A -B -C -g -t -T -r -E --regexp --file --max-count --after-context --before-context --context --color --glob --iglob --type --type-not --encoding --max-depth --sort --sortr --replace"
		} else {
			flags = "-n -i -v -w -x -l -L -c -o -q -s -h -H -r -R -a -I -F -E -P --line-number --ignore-case --invert-match --word-regexp --line-regexp --files-with-matches --files-without-match --count --only-matching --quiet --no-messages --no-filename --with-filename --text --fixed-strings"
			values = "-e -f -m -A -B -C --regexp --file --max-count --after-context --before-context --context --include --exclude --exclude-dir"
		}
		files, opts, ok := shellOptions(args, flags, values)
		pattern := len(opts["-e"])+len(opts["--regexp"])+len(opts["-f"])+len(opts["--file"]) > 0
		if program == "rg" && len(opts["--files"]) > 0 {
			return "list", nil, ok && !pattern
		}
		if !pattern {
			if len(files) == 0 {
				return "", nil, false
			}
			files = files[1:]
		}
		return "search", files, ok
	case "ls":
		_, _, ok := shellOptions(args, "-a -A -l -h -R -d -F -i -s -t -r -S -1 -C -x -p -L -H -n -u -c --all --almost-all --recursive --directory --human-readable", "--color --sort --time --ignore -I")
		return "list", nil, ok
	case "pwd":
		operands, _, ok := shellOptions(args, "-L -P", "")
		return "list", nil, ok && len(operands) == 0
	case "find":
		_, _, ok := shellOptions(args, "-H -L -P -print -print0 -empty -readable -writable -executable -depth -xdev -mount", "-type -name -iname -path -ipath -maxdepth -mindepth -size -mtime -mmin -newer")
		return "list", nil, ok
	case "go":
		if len(args) == 0 || (args[0] != "build" && args[0] != "test") {
			return "", nil, false
		}
		kind := args[0]
		_, _, ok := shellOptions(args[1:], "-a -n -race -msan -asan -v -work -x -trimpath -cover -c -i -json -short -failfast -fullpath", "-C -o -p -asmflags -buildmode -compiler -gccgoflags -gcflags -installsuffix -ldflags -mod -modfile -overlay -pgo -pkgdir -tags -toolexec -run -bench -benchtime -count -cpu -parallel -timeout -covermode -coverpkg -coverprofile -shuffle -vet")
		return kind, nil, ok
	case "cargo":
		if len(args) == 0 || (args[0] != "build" && args[0] != "test") {
			return "", nil, false
		}
		_, _, ok := shellOptions(args[1:], "-q -v --quiet --verbose --release --workspace --all --all-features --no-default-features --all-targets --lib --bins --examples --tests --benches --locked --offline --frozen --no-run", "-p -j --package --jobs --bin --example --test --bench --target --target-dir --manifest-path --features --profile --exclude --color --message-format")
		return args[0], nil, ok
	case "make":
		targets, _, ok := shellOptions(args, "-s --silent --quiet -n --just-print", "-j -C -f --jobs --directory --file")
		for _, target := range targets {
			if target != "all" && target != "build" {
				return "", nil, false
			}
		}
		return "build", nil, ok
	case "npm", "pnpm", "yarn":
		if len(args) > 1 && args[0] == "run" {
			args = args[1:]
		}
		if len(args) == 0 || (args[0] != "build" && args[0] != "test") {
			return "", nil, false
		}
		_, _, ok := shellOptions(args[1:], "--silent --quiet --if-present --watch --runInBand", "--workspace -w")
		return args[0], nil, ok
	case "python", "python3":
		if len(args) < 2 || args[0] != "-m" || args[1] != "pytest" {
			return "", nil, false
		}
		return shellCommand("pytest", args[2:])
	case "pytest":
		_, _, ok := shellOptions(args, "-q -v -s -x --quiet --verbose --exitfirst --collect-only --disable-warnings --lf --ff", "-k -m -n --maxfail --tb --rootdir --confcutdir --ignore --junitxml --durations")
		return "test", nil, ok
	case "git":
		for len(args) >= 2 && args[0] == "-C" {
			args = args[2:]
		}
		if len(args) == 0 {
			return "", nil, false
		}
		switch args[0] {
		case "status", "diff", "log", "show", "ls-files", "ls-tree", "rev-parse", "branch", "add", "commit", "checkout", "switch", "restore", "reset", "fetch", "pull", "push", "merge", "rebase", "tag", "stash":
			return "git", nil, true
		}
	}
	return "", nil, false
}
