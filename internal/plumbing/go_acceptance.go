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
// declarations. The host must select it explicitly; it supplies no runtime evidence.
type GoRepositoryAcceptanceAdapter struct{}

func (GoRepositoryAcceptanceAdapter) Select(ctx context.Context, part repository.Part, test Test) (RepositoryAcceptanceSelection, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	selection, _, err := goAcceptanceCommand(part, test)
	if err = errors.Join(err, ctx.Err()); err != nil {
		return RepositoryAcceptanceSelection{}, err
	}
	return selection, nil
}

// goAcceptanceCommand retains the declared argument boundaries. Only the
// inclusive package scope and placeholder change; an absent count becomes one.
// Build flags also govern discovery and compilation in the prepared toolchain.
func goAcceptanceCommand(part repository.Part, test Test) (RepositoryAcceptanceSelection, []string, error) {
	refuse := func(reason string) (RepositoryAcceptanceSelection, []string, error) {
		return RepositoryAcceptanceSelection{}, nil, errors.New(reason)
	}
	if part.Language != "go" || test.Framework != "go" || test.Part != part.Name {
		return refuse("Go acceptance needs its captured Go owner and framework")
	}
	if err := test.validateNative(); err != nil {
		return refuse(err.Error())
	}
	file, err := goAcceptanceFile(part, test.File)
	if err != nil {
		return refuse(err.Error())
	}
	pkg := path.Dir(file)
	if pkg != "." {
		pkg = "./" + pkg
	}
	command := part.Commands["test_one"]
	argv := slices.Clone(command.Argv)
	if command.NotApplicable != "" || len(argv) < 2 || argv[0] != "go" || argv[1] != "test" {
		return refuse("Go acceptance supports only direct literal go test arguments")
	}
	for _, arg := range argv {
		if !nativeText(arg) {
			return refuse("Go acceptance arguments must be nonempty visible text")
		}
	}
	components := strings.Split(test.Selector, "/")
	for i := range components {
		components[i] = "^" + regexp.QuoteMeta(components[i]) + "$"
	}
	pattern := strings.Join(components, "/")
	scope, scopeIndex := ".", -1
	seen := map[string]bool{}
	var build []string
	for i := 2; i < len(argv); i++ {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			if scopeIndex >= 0 {
				return refuse("Go acceptance supports at most one local package scope")
			}
			scope, scopeIndex = arg, i
			continue
		}
		name, value, equals := strings.Cut(arg, "=")
		if seen[name] {
			return refuse("Go acceptance flag repeats: " + name)
		}
		seen[name] = true
		start := i
		switch name {
		case "-short", "-v", "-race", "-trimpath":
			if equals && value != "true" && value != "false" {
				return refuse("Go acceptance boolean must be true or false: " + name)
			}
		case "-run", "-tags", "-timeout", "-parallel", "-p", "-cpu", "-shuffle", "-mod", "-count":
			if !equals {
				i++
				if i == len(argv) {
					return refuse("Go acceptance flag needs a value: " + name)
				}
				value = argv[i]
			}
		default:
			return refuse("unsupported Go acceptance flag: " + name)
		}
		valid := true
		switch name {
		case "-run":
			valid = value == "{test}" || value == "^{test}$"
			if equals {
				argv[i] = "-run=" + pattern
			} else {
				argv[i] = pattern
			}
		case "-count":
			valid = value == "1"
		case "-mod":
			valid = value == "readonly" || value == "vendor"
		case "-timeout":
			duration, err := time.ParseDuration(value)
			valid = err == nil && duration >= 0
		case "-parallel", "-p", "-cpu":
			n, err := strconv.Atoi(value)
			valid = err == nil && n > 0
		case "-shuffle":
			_, err := strconv.ParseInt(value, 10, 64)
			valid = value == "off" || value == "on" || err == nil
		case "-tags":
			valid = value != "" && strings.IndexFunc(value, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_., ", r)
			}) < 0 && len(strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' })) > 0
		}
		if !valid {
			return refuse(fmt.Sprintf("unsupported Go acceptance value for %s: %q", name, value))
		}
		switch name {
		case "-tags", "-p", "-race", "-trimpath", "-mod":
			build = append(build, argv[start:i+1]...)
		}
	}
	if !seen["-run"] {
		return refuse("Go acceptance needs exactly one -run {test} or -run ^{test}$")
	}
	if !goAcceptanceScope(scope, pkg) {
		return refuse(fmt.Sprintf("Go acceptance scope %q does not provably include %q", scope, pkg))
	}
	if scopeIndex >= 0 {
		argv[scopeIndex] = pkg
	}
	if !seen["-count"] {
		argv = append(argv, "-count=1")
	}
	return RepositoryAcceptanceSelection{Package: pkg, Argv: argv}, build, nil
}

func goAcceptanceFile(part repository.Part, file string) (string, error) {
	if !goAcceptancePath(file) || part.Directory != "." && !goAcceptancePath(part.Directory) {
		return "", errors.New("Go acceptance needs clean repository paths")
	}
	if part.Directory == "." {
		return file, nil
	}
	rel, ok := strings.CutPrefix(file, part.Directory+"/")
	if !ok || rel == "" {
		return "", errors.New("Go acceptance file is outside its captured part")
	}
	return rel, nil
}

func goAcceptancePath(name string) bool {
	if name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || path.Clean(name) != name || !nativeText(name) || strings.ContainsAny(name, `\*?[]:{}"`) {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".git" || strings.Contains(component, "...") {
			return false
		}
	}
	return true
}

func goAcceptanceScope(scope, pkg string) bool {
	if scope == "." {
		return pkg == "."
	}
	if !strings.HasPrefix(scope, "./") {
		return false
	}
	recursive := strings.HasSuffix(scope, "/...")
	base := strings.TrimSuffix(scope, "/...")
	if base != "." && !goAcceptancePath(strings.TrimPrefix(base, "./")) {
		return false
	}
	if base == pkg {
		return true
	}
	if !recursive || !strings.HasPrefix(pkg, base+"/") {
		return false
	}
	for _, dir := range strings.Split(strings.TrimPrefix(pkg, base+"/"), "/") {
		if dir == "vendor" || dir == "testdata" || strings.HasPrefix(dir, ".") || strings.HasPrefix(dir, "_") {
			return false
		}
	}
	return true
}
