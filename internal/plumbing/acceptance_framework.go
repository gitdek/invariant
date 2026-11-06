package plumbing

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/gitdek/invariant/internal/repository"
)

// acceptanceFramework shares structural rules, not execution support. The
// adapter separately admits captured commands and requires runtime evidence.
type acceptanceFramework struct {
	language        string
	displayLanguage func(string) string
	fileSelector    func(Test) bool
	selectorFile    bool
	declarations    func(string, string, []Test) error
	adapter         RepositoryAcceptanceAdapter
}

var acceptanceFrameworks = map[string]acceptanceFramework{
	"go": {
		language: "go", fileSelector: goFileSelector,
		declarations: nativeGoDeclarations, adapter: GoRepositoryAcceptanceAdapter{},
	},
	"pytest": {
		language: "python", fileSelector: pytestFileSelector, selectorFile: true,
		adapter: PytestRepositoryAcceptanceAdapter{},
	},
	"vitest": {
		language: "typescript", displayLanguage: nodeSourceLanguage, fileSelector: nodeFileSelector,
		adapter: VitestRepositoryAcceptanceAdapter{},
	},
	"jest": {
		language: "typescript", displayLanguage: nodeSourceLanguage, fileSelector: nodeFileSelector,
		adapter: JestRepositoryAcceptanceAdapter{},
	},
}

func lookupAcceptanceFramework(name string) (acceptanceFramework, bool) {
	definition, ok := acceptanceFrameworks[name]
	return definition, ok
}

func (f acceptanceFramework) sourceLanguage(file string) string {
	if f.displayLanguage != nil {
		return f.displayLanguage(file)
	}
	return f.language
}

func (f acceptanceFramework) validateFileSelector(test Test) error {
	if !f.fileSelector(test) {
		return fmt.Errorf("invalid native acceptance file or selector: %s", test.Identity())
	}
	return nil
}

func (f acceptanceFramework) validateOwner(owner repository.Part, test Test) error {
	if owner.Language != f.language || len(owner.Commands["test_one"].Argv) == 0 || owner.Commands["test_one"].NotApplicable != "" {
		return fmt.Errorf("part %q needs language %s and an applicable test_one for %s", owner.Name, f.language, test.Identity())
	}
	if f.selectorFile {
		rel := test.File
		if owner.Directory != "." {
			rel = strings.TrimPrefix(rel, owner.Directory+"/")
		}
		file, _, _ := strings.Cut(test.Selector, "::")
		if file != rel {
			return fmt.Errorf("pytest selector must start with part-relative file %s::", rel)
		}
	}
	return nil
}

func (f acceptanceFramework) checkDeclarations(file, src string, tests []Test) error {
	if !utf8.ValidString(src) {
		return fmt.Errorf("native acceptance file %s must be UTF-8", file)
	}
	if f.declarations != nil {
		return f.declarations(file, src, tests)
	}
	return nil
}

func (f acceptanceFramework) defaultAdapter() RepositoryAcceptanceAdapter { return f.adapter }

func goFileSelector(test Test) bool {
	segments := strings.Split(test.Selector, "/")
	valid := strings.HasSuffix(test.File, "_test.go") && testName.MatchString(segments[0])
	for _, segment := range segments {
		valid = valid && strings.TrimSpace(segment) != ""
	}
	return valid
}

func pytestFileSelector(test Test) bool {
	base := path.Base(test.File)
	valid := strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py"))
	_, node, found := strings.Cut(test.Selector, "::")
	// A function or concrete parameter case, never a file or class. Discovery
	// and the stricter node grammar belong to the execution adapter.
	declaration, _, _ := strings.Cut(node, "[")
	segments := strings.Split(declaration, "::")
	valid = valid && found && strings.HasPrefix(segments[len(segments)-1], "test")
	for _, segment := range segments {
		valid = valid && segment != ""
	}
	return valid
}

func nodeFileSelector(test Test) bool {
	ext := path.Ext(test.File)
	switch ext {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		stem := strings.TrimSuffix(test.File, ext)
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec") || strings.Contains("/"+test.File, "/__tests__/")
	}
	return false
}

func nodeSourceLanguage(file string) string {
	switch path.Ext(file) {
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	}
	return "typescript"
}
