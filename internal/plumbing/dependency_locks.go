package plumbing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gitdek/invariant/internal/repository"
)

// Admission is deliberately smaller than the package managers' command
// languages. A loader may retain other declarations, but preparation never
// turns a wrapper, option file, local path or recursive include into authority.
func dependencyInstall(p repository.Part, files map[string][]byte) ([]string, error) {
	c := p.Commands["install"]
	if c.NotApplicable != "" {
		if len(files) != 0 {
			return nil, errors.New("install.not_applicable requires empty dependency inputs")
		}
		return nil, nil
	}
	for name := range files {
		switch path.Base(name) {
		case ".npmrc", ".yarnrc", ".yarnrc.yml", "go.work", "go.work.sum", "pip.conf", "pip.ini":
			return nil, fmt.Errorf("unsupported dependency configuration %s", name)
		}
	}
	switch p.Language {
	case "go":
		if !slices.Equal(c.Argv, []string{"go", "mod", "download"}) {
			return nil, errors.New("Go preparation supports only go mod download")
		}
		if err := goDependencyLock(p, files); err != nil {
			return nil, err
		}
		return slices.Clone(c.Argv), nil
	case "python":
		return pythonDependencyLock(p, files)
	case "typescript":
		if len(c.Argv) < 2 || c.Argv[0] != "npm" || c.Argv[1] != "ci" {
			return nil, errors.New("TypeScript preparation supports only npm ci")
		}
		for _, arg := range c.Argv[2:] {
			if !slices.Contains([]string{"--ignore-scripts", "--no-audit", "--no-fund", "--quiet"}, arg) {
				return nil, fmt.Errorf("unsupported npm ci option %q", arg)
			}
		}
		if err := npmDependencyLock(p, files); err != nil {
			return nil, err
		}
		return append(slices.Clone(c.Argv), "--ignore-scripts", "--no-audit", "--no-fund"), nil
	}
	return nil, fmt.Errorf("unsupported dependency language %q", p.Language)
}

var goDependencyVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+incompatible)?$`)
var goLanguageVersion = regexp.MustCompile(`^1\.[0-9]+(\.[0-9]+)?$`)

func goDependencyLock(p repository.Part, files map[string][]byte) error {
	mod, sum := path.Join(p.Directory, "go.mod"), path.Join(p.Directory, "go.sum")
	if !slices.Contains(p.Inputs, mod) {
		return errors.New("Go preparation needs go.mod as a declared input")
	}
	required := map[string]string{}
	inRequire, moduleSeen, goSeen, toolchainSeen := false, false, false, false
	for _, line := range strings.Split(string(files[mod]), "\n") {
		line, _, _ = strings.Cut(line, "//")
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		if inRequire && len(words) == 1 && words[0] == ")" {
			inRequire = false
			continue
		}
		if inRequire {
			words = append([]string{"require"}, words...)
		}
		if len(words) == 2 && words[0] == "require" && words[1] == "(" && !inRequire {
			inRequire = true
			continue
		}
		switch {
		case len(words) == 2 && words[0] == "module" && !moduleSeen:
			moduleSeen = true
		case len(words) == 2 && words[0] == "go" && !goSeen && goLanguageVersion.MatchString(words[1]):
			goSeen = true
		case len(words) == 2 && words[0] == "toolchain" && !toolchainSeen && (words[1] == "default" || goLanguageVersion.MatchString(strings.TrimPrefix(words[1], "go"))):
			toolchainSeen = true
		case len(words) == 3 && words[0] == "require" && goDependencyVersion.MatchString(words[2]):
			name := words[1]
			if strings.HasPrefix(name, "\"") {
				var err error
				name, err = strconv.Unquote(name)
				if err != nil {
					return err
				}
			}
			if name == "" || strings.ContainsAny(name, "\\:@ \t") || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") || required[name] != "" {
				return fmt.Errorf("unsupported or duplicate Go module %q", name)
			}
			required[name] = words[2]
		default:
			return fmt.Errorf("unsupported or unpinned go.mod declaration %q (replacements, exclusions and workspaces are not supported)", strings.TrimSpace(line))
		}
	}
	if !moduleSeen || !goSeen || inRequire {
		return errors.New("incomplete go.mod")
	}
	if len(required) != 0 && !slices.Contains(p.Lockfiles, sum) {
		return errors.New("Go dependencies require a declared go.sum")
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(files[sum]), "\n") {
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		if len(words) != 3 || !goDependencyVersion.MatchString(strings.TrimSuffix(words[1], "/go.mod")) || !strings.HasPrefix(words[2], "h1:") {
			return fmt.Errorf("invalid go.sum entry %q", line)
		}
		hash, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(words[2], "h1:"))
		if err != nil || len(hash) != 32 {
			return errors.New("go.sum needs SHA-256 module hashes")
		}
		key := words[0] + " " + words[1]
		if old, ok := sums[key]; ok && old != words[2] {
			return fmt.Errorf("conflicting go.sum hashes for %s", key)
		}
		sums[key] = words[2]
	}
	for name, version := range required {
		if sums[name+" "+version] == "" || sums[name+" "+version+"/go.mod"] == "" {
			return fmt.Errorf("go.sum does not cover %s %s and its go.mod", name, version)
		}
	}
	return nil
}

// pythonLockText refuses requirements text that pip could split or decode
// differently from the grammar below, so no line admission reads as a
// comment reaches pip as an option (#748): anything but tabs, line feeds and
// printable ASCII once CRLF is normalized, a coding declaration, a comment
// continued onto the next line, or a "#" that pip wouldn't read as a comment.
func pythonLockText(name string, data []byte) (string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	for i := 0; i < len(text); i++ {
		if c := text[i]; c != '\t' && c != '\n' && (c < 0x20 || c > 0x7e) {
			return "", fmt.Errorf("%s: requirements must be lines of printable ASCII, not byte %#02x at offset %d", name, c, i)
		}
	}
	for i, line := range strings.Split(text, "\n") {
		comment := strings.IndexByte(line, '#')
		switch {
		case comment < 0:
		case i < 2 && pythonCoding.MatchString(line):
			return "", fmt.Errorf("%s:%d: requirements can't declare an encoding", name, i+1)
		case comment > 0 && line[comment-1] != ' ' && line[comment-1] != '\t':
			return "", fmt.Errorf("%s:%d: a comment's \"#\" must start the line or follow whitespace", name, i+1)
		case strings.TrimSpace(line[:comment]) == "" && strings.HasSuffix(line, "\\"):
			return "", fmt.Errorf("%s:%d: a comment can't continue onto the next line", name, i+1)
		}
	}
	return text, nil
}

var pythonCoding = regexp.MustCompile(`(?i)coding[:=]`)

var pythonRequirement = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9_,.-]+\])?)==([0-9][A-Za-z0-9.!+_-]*)$`)
var pythonHash = regexp.MustCompile(`^--hash=sha256:[0-9a-fA-F]{64}$`)

func pythonDependencyLock(p repository.Part, files map[string][]byte) ([]string, error) {
	argv := p.Commands["install"].Argv
	if len(argv) < 4 || (argv[0] != "python3" && argv[0] != "python") || !slices.Equal(argv[1:4], []string{"-m", "pip", "install"}) {
		return nil, errors.New("Python preparation supports only python[3] -m pip install -r requirements")
	}
	used := map[string]bool{}
	for i := 4; i < len(argv); i++ {
		switch argv[i] {
		case "--require-hashes", "--only-binary=:all:", "--ignore-installed", "--quiet", "-q":
		case "-r", "--requirement":
			i++
			if i == len(argv) || path.IsAbs(argv[i]) || strings.ContainsAny(argv[i], "\\\x00") {
				return nil, errors.New("pip needs a repository-relative requirements path")
			}
			name := path.Join(p.Directory, argv[i])
			if !slices.Contains(p.Lockfiles, name) {
				return nil, fmt.Errorf("pip requirements %q is not a declared lockfile", name)
			}
			used[name] = true
		default:
			return nil, fmt.Errorf("unsupported pip option or requirement %q", argv[i])
		}
	}
	if len(used) == 0 || len(used) != len(p.Lockfiles) {
		return nil, errors.New("pip installation must name every declared requirements lockfile")
	}
	versions := map[string]string{}
	for name := range used {
		body, err := pythonLockText(name, files[name])
		if err != nil {
			return nil, err
		}
		// Continuations join hash lines only; all other pip directives,
		// markers, direct references and includes fail the grammar below.
		body = strings.ReplaceAll(body, "\\\n", " ")
		for _, line := range strings.Split(body, "\n") {
			line, _, _ = strings.Cut(line, "#")
			words := strings.Fields(line)
			if len(words) == 0 {
				continue
			}
			match := pythonRequirement.FindStringSubmatch(words[0])
			if len(match) == 0 || len(words) < 2 {
				return nil, fmt.Errorf("%s: requirements need exact versions and SHA-256 hashes; unsupported requirement %q", name, line)
			}
			pkg, _, _ := strings.Cut(strings.ToLower(match[1]), "[")
			pkg = strings.NewReplacer("_", "-", ".", "-").Replace(pkg)
			if previous := versions[pkg]; previous != "" && previous != match[3] {
				return nil, fmt.Errorf("conflicting Python versions for %s", pkg)
			}
			versions[pkg] = match[3]
			for _, flag := range words[1:] {
				if !pythonHash.MatchString(flag) {
					return nil, fmt.Errorf("%s: unsupported hash or requirement option %q", name, flag)
				}
			}
		}
	}
	// Even bootstrap packages in the venv must be fetched and hash-checked.
	return append(slices.Clone(argv), "--require-hashes", "--only-binary=:all:", "--ignore-installed", "--isolated", "--no-input", "--disable-pip-version-check", "--no-cache-dir"), nil
}

type npmDependencyPackage struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Integrity            string            `json:"integrity"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
	Workspaces          json.RawMessage `json:"workspaces"`
	Overrides           json.RawMessage `json:"overrides"`
	BundledDependencies json.RawMessage `json:"bundledDependencies"`
	BundleDependencies  json.RawMessage `json:"bundleDependencies"`
	Link                bool            `json:"link"`
	InBundle            bool            `json:"inBundle"`
}

var npmPackageName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
var npmExactVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

func npmDependencyLock(p repository.Part, files map[string][]byte) error {
	manifest, lock := path.Join(p.Directory, "package.json"), path.Join(p.Directory, "package-lock.json")
	if !slices.Contains(p.Inputs, manifest) || !slices.Contains(p.Lockfiles, lock) || len(p.Lockfiles) != 1 {
		return errors.New("npm ci needs package.json and one package-lock.json v2/v3")
	}
	// npm-shrinkwrap takes precedence over package-lock even with npm ci.
	for name := range files {
		if path.Base(name) == "npm-shrinkwrap.json" {
			return errors.New("npm shrinkwrap is not supported")
		}
	}
	var pkg npmDependencyPackage
	var locked struct {
		Name            string                          `json:"name"`
		Version         string                          `json:"version"`
		LockfileVersion int                             `json:"lockfileVersion"`
		Packages        map[string]npmDependencyPackage `json:"packages"`
	}
	if err := dependencyJSON(files[manifest], &pkg); err != nil {
		return fmt.Errorf("package.json: %w", err)
	}
	if err := dependencyJSON(files[lock], &locked); err != nil {
		return fmt.Errorf("package-lock.json: %w", err)
	}
	root, ok := locked.Packages[""]
	if !ok || (locked.LockfileVersion != 2 && locked.LockfileVersion != 3) {
		return errors.New("npm needs a v2/v3 lock with its root package")
	}
	if pkg.Name != root.Name || pkg.Version != root.Version || pkg.Name != locked.Name || pkg.Version != locked.Version || !maps.Equal(pkg.Dependencies, root.Dependencies) || !maps.Equal(pkg.DevDependencies, root.DevDependencies) || !maps.Equal(pkg.OptionalDependencies, root.OptionalDependencies) || !maps.Equal(pkg.PeerDependencies, root.PeerDependencies) {
		return errors.New("npm manifest and lock root disagree")
	}
	if err := npmSupportedPackage(pkg, true); err != nil {
		return err
	}
	for location, node := range locked.Packages {
		if err := npmSupportedPackage(node, location == ""); err != nil {
			return fmt.Errorf("npm package %q: %w", location, err)
		}
		if location != "" {
			// Each location is a chain of node_modules/package components;
			// no workspace targets, links or filesystem paths are admitted.
			for _, component := range strings.Split("/"+location, "/node_modules/")[1:] {
				if !npmPackageName.MatchString(component) {
					return fmt.Errorf("unsupported npm lock location %q", location)
				}
			}
			if !strings.HasPrefix(location, "node_modules/") || !npmExactVersion.MatchString(node.Version) {
				return fmt.Errorf("unsupported or floating npm package %q", location)
			}
			u, err := url.Parse(node.Resolved)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !npmIntegrity(node.Integrity) {
				return fmt.Errorf("npm package %q needs an HTTPS artifact and SHA-256/512 integrity", location)
			}
		}
		groups := []map[string]string{node.Dependencies, node.OptionalDependencies, node.PeerDependencies}
		if location == "" {
			groups = append(groups, node.DevDependencies)
		}
		bundled, all, err := npmBundled(node)
		if err != nil {
			return fmt.Errorf("npm package %q: %w", location, err)
		}
		for group, deps := range groups {
			for name, spec := range deps {
				if !npmPackageName.MatchString(name) {
					return fmt.Errorf("unsupported npm dependency name %q", name)
				}
				// A bundled dependency comes from the package's own tarball,
				// not from the lock's tree.
				if all || bundled[name] {
					continue
				}
				// optionalDependencies override dependencies of the same name.
				if group == 0 && node.OptionalDependencies[name] != "" {
					continue
				}
				target, found := npmResolve(locked.Packages, location, name)
				if !found && group == 2 && node.PeerDependenciesMeta[name].Optional {
					if _, err := npmRange("0.0.0", spec); err != nil {
						return err
					}
					continue
				}
				if !found {
					return fmt.Errorf("npm lock lacks %s required by %q", name, location)
				}
				matches, err := npmRange(target.Version, spec)
				if err != nil || !matches {
					return fmt.Errorf("npm lock version %s does not satisfy %s %q: %v", target.Version, name, spec, err)
				}
			}
		}
	}
	return nil
}

// A dependency may bundle packages: they ship inside its own tarball, which
// its integrity covers. Packages recorded as bundled inside another are still
// refused, as is bundling at the root.
func npmSupportedPackage(p npmDependencyPackage, root bool) error {
	bundles := len(p.BundledDependencies) != 0 || len(p.BundleDependencies) != 0
	if len(p.Workspaces) != 0 || len(p.Overrides) != 0 || bundles && root || p.Link || p.InBundle {
		return errors.New("npm workspaces, overrides, bundled packages and links are not supported")
	}
	return nil
}

// npmBundled names the dependencies a package ships inside its own tarball:
// a list of names, or true for all of them.
func npmBundled(p npmDependencyPackage) (map[string]bool, bool, error) {
	names, all := map[string]bool{}, false
	for _, raw := range []json.RawMessage{p.BundleDependencies, p.BundledDependencies} {
		if len(raw) == 0 {
			continue
		}
		var list []string
		if err := json.Unmarshal(raw, &list); err == nil {
			for _, name := range list {
				names[name] = true
			}
			continue
		}
		if err := json.Unmarshal(raw, &all); err != nil {
			return nil, false, errors.New("bundled dependencies must be a list of names or a boolean")
		}
	}
	return names, all, nil
}

func npmIntegrity(value string) bool {
	words := strings.Fields(value)
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		algorithm, encoded, _ := strings.Cut(word, "-")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !((algorithm == "sha512" && len(data) == 64) || (algorithm == "sha256" && len(data) == 32)) {
			return false
		}
	}
	return true
}

func npmResolve(packages map[string]npmDependencyPackage, location, name string) (npmDependencyPackage, bool) {
	for {
		if p, ok := packages[path.Join(location, "node_modules", name)]; ok {
			return p, true
		}
		if location == "" || location == "." {
			return npmDependencyPackage{}, false
		}
		location = path.Dir(location)
	}
}

// npmRange admits registry semver ranges, never tags, aliases or URL/path
// specifications. npm ci remains the final check against the pinned npm's
// resolver; this check catches incomplete/inconsistent declarations early.
func npmRange(version, spec string) (bool, error) {
	v, _, err := npmVersion(version)
	if err != nil {
		return false, err
	}
	matched := false
	for _, alternative := range strings.Split(spec, "||") {
		words := strings.Fields(alternative)
		if len(words) == 3 && words[1] == "-" {
			words = []string{">=" + words[0], "<=" + words[2]}
		}
		all := true
		prerelease := v.pre == ""
		for i := 0; i < len(words); i++ {
			word := words[i]
			operator := ""
			for len(word) > 0 && strings.ContainsRune("<>=^~", rune(word[0])) {
				operator += word[:1]
				word = word[1:]
			}
			if word == "" && i+1 < len(words) {
				i++
				word = words[i]
			}
			base, width, err := npmVersion(word)
			if err != nil || !slices.Contains([]string{"", "=", "<", "<=", ">", ">=", "^", "~"}, operator) {
				return false, fmt.Errorf("unsupported npm version range %q", spec)
			}
			if base.pre != "" && v.n == base.n {
				prerelease = true
			}
			comparison := npmCompare(v, base)
			upper := base
			upper.pre = ""
			if width < 3 && width > 0 {
				upper.n[width-1]++
			}
			ok := false
			switch operator {
			case "", "=":
				ok = width == 0 || (width == 3 && comparison == 0) || (width < 3 && comparison >= 0 && npmCompare(v, upper) < 0)
			case ">=":
				ok = comparison >= 0
			case ">":
				ok = (width == 3 && comparison > 0) || (width > 0 && width < 3 && npmCompare(v, upper) >= 0)
			case "<":
				ok = comparison < 0
			case "<=":
				ok = width == 0 || (width == 3 && comparison <= 0) || (width < 3 && npmCompare(v, upper) < 0)
			case "^", "~":
				index := 0
				if operator == "~" && width > 1 {
					index = 1
				} else if operator == "^" {
					for index < width-1 && base.n[index] == 0 {
						index++
					}
				}
				upper = base
				upper.pre = ""
				upper.n[index]++
				for j := index + 1; j < 3; j++ {
					upper.n[j] = 0
				}
				ok = width == 0 || (comparison >= 0 && npmCompare(v, upper) < 0)
			}
			all = all && ok
		}
		matched = matched || (all && prerelease)
	}
	return matched, nil
}

type npmSemver struct {
	n   [3]uint64
	pre string
}

func npmVersion(text string) (npmSemver, int, error) {
	var v npmSemver
	text = strings.TrimPrefix(text, "v")
	text, _, _ = strings.Cut(text, "+")
	text, v.pre, _ = strings.Cut(text, "-")
	fields := strings.Split(text, ".")
	if len(fields) > 3 {
		return v, 0, errors.New("invalid semver")
	}
	width, wildcard := 0, false
	for i, f := range fields {
		if f == "*" || strings.EqualFold(f, "x") {
			wildcard = true
			continue
		}
		n, err := strconv.ParseUint(f, 10, 32)
		if err != nil || wildcard || (len(f) > 1 && f[0] == '0') {
			return v, 0, errors.New("invalid semver")
		}
		v.n[i] = n
		width++
	}
	if v.pre != "" && (width != 3 || !npmExactVersion.MatchString(text+"-"+v.pre)) {
		return v, 0, errors.New("invalid prerelease")
	}
	return v, width, nil
}

func npmCompare(a, b npmSemver) int {
	for i := range a.n {
		if a.n[i] < b.n[i] {
			return -1
		}
		if a.n[i] > b.n[i] {
			return 1
		}
	}
	if a.pre == b.pre {
		return 0
	}
	if a.pre == "" {
		return 1
	}
	if b.pre == "" {
		return -1
	}
	x, y := strings.Split(a.pre, "."), strings.Split(b.pre, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] == y[i] {
			continue
		}
		xn, xe := strconv.ParseUint(x[i], 10, 64)
		yn, ye := strconv.ParseUint(y[i], 10, 64)
		if xe == nil && ye == nil {
			if xn < yn {
				return -1
			}
			return 1
		}
		if xe == nil {
			return -1
		}
		if ye == nil {
			return 1
		}
		return strings.Compare(x[i], y[i])
	}
	if len(x) < len(y) {
		return -1
	}
	return 1
}

// Reject duplicate keys and case aliases of typed fields. Go's JSON decoder
// accepts case-insensitive field names; npm interprets only the exact spelling.
func dependencyJSON(data []byte, value any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var scan func(reflect.Type) error
	scan = func(typ reflect.Type) error {
		for typ != nil && typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		token, err := dec.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				key, err := dec.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("invalid or duplicate JSON key %q", key)
				}
				seen[name] = true
				var fieldType reflect.Type
				if typ != nil && typ.Kind() == reflect.Map {
					fieldType = typ.Elem()
				} else if typ != nil && typ.Kind() == reflect.Struct {
					for i := 0; i < typ.NumField(); i++ {
						field := typ.Field(i)
						canonical, _, _ := strings.Cut(field.Tag.Get("json"), ",")
						if canonical == "" {
							canonical = field.Name
						}
						if strings.EqualFold(name, canonical) {
							if name != canonical {
								return fmt.Errorf("JSON key %q must be spelled %q", name, canonical)
							}
							fieldType = field.Type
							break
						}
					}
				}
				if err := scan(fieldType); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		case json.Delim('['):
			var element reflect.Type
			if typ != nil && (typ.Kind() == reflect.Array || typ.Kind() == reflect.Slice) {
				element = typ.Elem()
			}
			for dec.More() {
				if err := scan(element); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		}
		return err
	}
	if err := scan(reflect.TypeOf(value)); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing dependency JSON")
	}
	return json.Unmarshal(data, value)
}
