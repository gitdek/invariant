package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/toolchain"
)

// Validate raw bytes before BOM removal, newline normalization, comments,
// continuations or include parsing. Pip discovers an encoding from its first
// two raw LF-delimited lines; accepting a declaration after normalizing CR
// gives the validator a different language from the network-enabled installer.
// Every effective requirements/constraints file therefore uses valid UTF-8,
// an optional leading UTF-8 BOM, LF/CRLF only, and no encoding declaration
// anywhere. The existing splitlines-separator rejection remains in force.
//
// The default layer captures the recipe and checks canonical installer bytes
// plus both original and canonical provenance. The opt-in layer actually
// executes that generated recipe:
// INVARIANT_PYTHON_DEPS_REAL=1 go test ./internal/verify -run '^TestPythonDependenciesAcceptUTF8Inputs$' -count=1 -timeout 30m
func TestPythonDependenciesAcceptUTF8Inputs(t *testing.T) {
	var canonicalUTF8 map[string]string
	for _, tc := range []struct {
		name    string
		bom     string
		newline string
	}{
		{"LF", "", "\n"},
		{"CRLF", "", "\r\n"},
		{"BOM LF", "\xef\xbb\xbf", "\n"},
		{"BOM CRLF", "\xef\xbb\xbf", "\r\n"},
		{"mixed LF and CRLF", "\xef\xbb\xbf", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pyDepsFixture(t)
			inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"}
			for _, filename := range inputs {
				b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filename)))
				if err != nil {
					t.Fatal(err)
				}
				// Non-ASCII comments establish UTF-8 acceptance, rather than
				// merely the ASCII subset. A unique comment forces real builds.
				body := fmt.Sprintf("# café λ 日本語 %s %d\n", tc.name, time.Now().UnixNano()) + string(b)
				var encoded strings.Builder
				encoded.WriteString(tc.bom)
				for i, line := range strings.SplitAfter(body, "\n") {
					newline := tc.newline
					if newline == "" {
						newline = "\n"
						if i%2 == 0 {
							newline = "\r\n"
						}
					}
					encoded.WriteString(strings.ReplaceAll(line, "\n", newline))
				}
				pyDepsWrite(t, root, filename, encoded.String())
			}
			before := pyDepsFiles(t, root)
			t.Run("generated recipe contract", func(t *testing.T) {
				pyDepsFakeDocker(t)
				first := pyDepsBuild(t, root)
				pyDepsAssertRecipeEnvironment(t, root, first, inputs)
				canonical := pyDepsAssertCanonicalStaging(t, root, first, inputs)
				if canonicalUTF8 == nil {
					canonicalUTF8 = canonical
				} else if !reflect.DeepEqual(canonical, canonicalUTF8) {
					t.Fatalf("BOM, newline style or comments changed canonical installation inputs: got=%#v want=%#v", canonical, canonicalUTF8)
				}
				if second := pyDepsBuild(t, root); !reflect.DeepEqual(first, second) || pyDepsBuildCount(t) != 1 {
					t.Fatalf("accepted raw input bytes did not reuse their environment: first=%#v second=%#v", first, second)
				}
			})
			if os.Getenv("INVARIANT_PYTHON_DEPS_REAL") == "1" {
				t.Run("execute generated recipe", func(t *testing.T) {
					pyDepsRequireRealDocker(t)
					env := pyDepsBuild(t, root)
					pyDepsCleanupRecipeImage(t, env.Image)
					pyDepsAssertRecipeEnvironment(t, root, env, inputs)
					pyDepsRunChecks(t, root)
				})
			}
			if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
				t.Fatal("accepting UTF-8 inputs rewrote their bytes")
			}
		})
	}
}

type pyDepsInvalidInputCase struct {
	name string
	raw  string
}

func TestPythonDependenciesRejectInvalidUTF8(t *testing.T) {
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"Latin-1 comment", "# caf\xe9\npackaging==24.2\n"},
		{"stray continuation byte", "# \x80\npackaging==24.2\n"},
		{"truncated multibyte sequence", "# \xe2\x82\npackaging==24.2\n"},
		{"overlong LF", "# \xc0\x8a--no-binary :all:\npackaging==24.2\n"},
		{"UTF-8 surrogate", "# \xed\xa0\x80\npackaging==24.2\n"},
		{"beyond Unicode range", "# \xf4\x90\x80\x80\npackaging==24.2\n"},
		{"invalid after valid BOM", "\xef\xbb\xbf# \xff\npackaging==24.2\n"},
		{"UTF-16 BOM", "\xff\xfe#\x00 \x00x\x00\n\x00"},
	})
}

func TestPythonDependenciesRejectBareCR(t *testing.T) {
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"CR only lines", "# ordinary comment\rpackaging==24.2\r"},
		{"inside comment", "# first\r# second\npackaging==24.2\n"},
		{"trailing CR", "packaging==24.2\n# end\r"},
		{"repeated CR before LF", "# comment\r\r\npackaging==24.2\n"},
		{"before continuation", "# comment \\\r# continued\npackaging==24.2\n"},
		{"mixed valid and bare CR", "# valid\r\n# invalid\r# after\npackaging==24.2\n"},
		{"after BOM", "\xef\xbb\xbf# first\r# second\npackaging==24.2\n"},
	})
}

func TestPythonDependenciesRejectEncodingDeclarations(t *testing.T) {
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"UTF-8 first line", "# coding: utf-8\npackaging==24.2\n"},
		{"equals second line", "# requirements\n# coding=utf-8\npackaging==24.2\n"},
		{"Emacs declaration", "# -*- coding: utf-8 -*-\npackaging==24.2\n"},
		{"Vim declaration", "# vim: set fileencoding=utf-8 :\npackaging==24.2\n"},
		{"third line", "# one\n# two\n# coding: unicode_escape\npackaging==24.2\n"},
		{"late declaration", "packaging==24.2\n\n# ordinary\n# coding: utf-8\n"},
		{"trailing comment", "packaging==24.2 # coding: utf-8\n"},
		{"CRLF declaration", "# ordinary\r\n# coding: utf-8\r\npackaging==24.2\r\n"},
		{"BOM declaration", "\xef\xbb\xbf# coding: utf-8\npackaging==24.2\n"},
		{"non UTF-8 declaration", "# coding: latin-1\npackaging==24.2\n"},
		{"unicode escape hidden source option", "# coding: unicode_escape\n# \\u000a--no-binary :all:\npackaging\n"},
		{"unicode escape hidden index", "# coding=unicode_escape\n# \\u000a--index-url https://example.invalid/simple\npackaging\n"},
	})
}

func TestPythonDependenciesRejectEncodingDeclarationExploit(t *testing.T) {
	// Exact raw bytes from the #243 review: two bare CR bytes, then a
	// literal backslash-u sequence (not an already-decoded LF). Pip finds
	// coding: in its first raw LF-delimited line and unicode_escape turns
	// that sequence into an active option during the network-enabled build.
	const review = "# a\r# b\r# coding: unicode_escape\n# \\u000a--no-binary :all:\npackaging\n"
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"review raw bytes", review},
		{"review index redirection", strings.ReplaceAll(review, "--no-binary :all:", "--index-url https://example.invalid/simple")},
	})
}

// Encoding and strict grammar restrictions apply equally to the root, a
// recursively referenced requirement and a recursively referenced constraint.
// The Docker double records every CLI entry, including image inspection.
func pyDepsRejectInvalidInputs(t *testing.T, cases []pyDepsInvalidInputCase) {
	t.Helper()
	pyDepsFakeDocker(t)
	for _, tc := range cases {
		for _, filename := range []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"} {
			t.Run(tc.name+"/"+filename, func(t *testing.T) {
				root := pyDepsFixture(t)
				pyDepsWrite(t, root, filename, tc.raw)
				before := pyDepsFiles(t, root)
				if recipe, err := toolchain.PythonDepsRecipe(root); err == nil {
					t.Errorf("unsafe dependency input accepted in %s: recipe=%q input=%q", filename, recipe, tc.raw)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if env, err := toolchain.PythonDepsImage(ctx, root); err == nil || env != nil {
					t.Errorf("unsafe dependency input reached image selection: env=%#v error=%v", env, err)
				}
				if calls := pyDepsCalls(t); len(calls) != 0 {
					t.Fatalf("raw input validation must precede every Docker invocation: calls=%#v", calls)
				}
				if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
					t.Fatal("rejection rewrote dependency inputs")
				}
			})
		}
	}
}
