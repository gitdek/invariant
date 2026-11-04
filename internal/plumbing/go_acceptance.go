package plumbing

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gitdek/invariant/internal/repository"
)

// GoRepositoryAcceptanceAdapter validates captured commands and discovers Go
// declarations. It supplies no runtime evidence and is never enabled by default.
type GoRepositoryAcceptanceAdapter struct{}

func (GoRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	refuse := func(reason string) (RepositoryAcceptanceSelection, error) {
		return RepositoryAcceptanceSelection{}, errors.New(reason)
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	if part.Language != "go" || test.Framework != "go" || part.Name != test.Part {
		return refuse("Go acceptance needs its captured Go owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	dir := path.Dir(file)
	if strings.Contains(dir, "...") {
		return refuse("Go acceptance package cannot contain a package pattern")
	}
	pkg := "."
	if dir != "." {
		pkg = "./" + dir
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	if command.NotApplicable != "" || len(argv) < 2 || argv[0] != "go" || argv[1] != "test" {
		return refuse("Go acceptance supports only direct go test arguments")
	}
	for _, arg := range argv {
		if !nativeText(arg) {
			return refuse("Go acceptance command contains empty or invisible arguments")
		}
	}
	components := strings.Split(test.Selector, "/")
	for i := range components {
		components[i] = "^" + regexp.QuoteMeta(components[i]) + "$"
	}
	pattern := strings.Join(components, "/")
	seen := map[string]bool{}
	scope, scopeIndex := ".", -1
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			if scopeIndex >= 0 {
				return refuse("Go acceptance supports only one local package scope")
			}
			scope, scopeIndex = arg, i
			continue
		}
		flag, value, equal := strings.Cut(arg, "=")
		if seen[flag] {
			return refuse("Go acceptance refuses repeated flag " + flag)
		}
		seen[flag] = true
		boolean := false
		switch flag {
		case "-short", "-v", "-race", "-trimpath":
			boolean = true
		case "-run", "-tags", "-timeout", "-parallel", "-p", "-cpu", "-shuffle", "-mod", "-count":
		default:
			return refuse("Go acceptance does not support flag " + flag)
		}
		valueIndex := i
		if !boolean && !equal {
			i++
			if i == len(argv) {
				return refuse("Go acceptance flag needs a value: " + flag)
			}
			value, valueIndex = argv[i], i
		}
		valid := false
		switch flag {
		case "-short", "-v", "-race", "-trimpath":
			valid = !equal || value == "true" || value == "false"
		case "-run":
			valid = value == "{test}" || value == "^{test}$"
			if equal {
				argv[valueIndex] = "-run=" + pattern
			} else {
				argv[valueIndex] = pattern
			}
		case "-tags":
			valid = goAcceptanceTags(value)
		case "-timeout":
			duration, err := time.ParseDuration(value)
			valid = err == nil && duration >= 0
		case "-parallel", "-p", "-cpu":
			n, err := strconv.Atoi(value)
			valid = err == nil && n > 0
		case "-shuffle":
			_, err := strconv.ParseInt(value, 10, 64)
			valid = value == "on" || value == "off" || err == nil
		case "-mod":
			valid = value == "readonly" || value == "vendor"
		case "-count":
			valid = value == "1"
		}
		if !valid {
			return refuse(fmt.Sprintf("Go acceptance does not support %s value %q", flag, value))
		}
	}
	if !seen["-run"] {
		return refuse("Go acceptance needs exactly one -run placeholder")
	}
	if !goAcceptanceScope(scope, dir) {
		return refuse(fmt.Sprintf("Go acceptance scope %q does not provably include package %q", scope, pkg))
	}
	if scopeIndex >= 0 {
		argv[scopeIndex] = pkg
	} else {
		argv = append(argv, pkg)
	}
	if !seen["-count"] {
		argv = append(argv, "-count=1")
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	return RepositoryAcceptanceSelection{Package: pkg, Argv: argv}, nil
}

func goAcceptanceFile(part repository.Part, file string) (string, error) {
	if part.Directory != "." && !repositoryAcceptancePath(part.Directory) || !repositoryAcceptancePath(file) {
		return "", errors.New("Go acceptance needs clean owner and source paths")
	}
	if part.Directory == "." {
		return file, nil
	}
	rel, ok := strings.CutPrefix(file, part.Directory+"/")
	if !ok {
		return "", errors.New("Go acceptance source is outside its captured owner")
	}
	return rel, nil
}

func goAcceptanceScope(scope, dir string) bool {
	if scope == "." {
		return dir == "."
	}
	if !strings.HasPrefix(scope, "./") {
		return false
	}
	base := strings.TrimPrefix(scope, "./")
	recursive := base == "..." || strings.HasSuffix(base, "/...")
	if recursive {
		base = strings.TrimSuffix(base, "...")
		base = strings.TrimSuffix(base, "/")
		if base == "" {
			base = "."
		}
	}
	if base == "." && scope != "./..." {
		return false
	}
	if base != "." && !repositoryAcceptancePath(base) || strings.ContainsAny(base, "*?[]:{}") || strings.Contains(base, "...") || strings.HasSuffix(base, ".go") {
		return false
	}
	if !recursive {
		return base != "." && base == dir
	}
	// Go's recursive patterns skip these directories. Narrowing to an explicit
	// package must not turn an excluded subtree into an included package.
	if dir != "." {
		for _, component := range strings.Split(dir, "/") {
			if component == "vendor" || component == "testdata" || strings.HasPrefix(component, ".") || strings.HasPrefix(component, "_") {
				return false
			}
		}
	}
	return base == "." || dir == base || strings.HasPrefix(dir, base+"/")
}

func goAcceptanceTags(value string) bool {
	for _, tag := range strings.Split(value, ",") {
		if tag == "" {
			return false
		}
		for _, r := range tag {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' {
				return false
			}
		}
	}
	return true
}
