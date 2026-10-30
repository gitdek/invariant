package verify

import (
	"reflect"
	"strings"
	"testing"
)

// #253: pip removes a single trailing continuation without inserting or
// removing any other character. In particular, inserting a space inside
// 'posix' silently changes whether the dependency is installed. These are
// parser fixtures: synthetic hashes are checked offline by the real parser
// oracle, while the Docker double captures production's canonical bytes.
func TestPythonDependenciesJoinContinuationsWithoutInsertedCharacters(t *testing.T) {
	firstHash, secondHash := strings.Repeat("a", 32), strings.Repeat("b", 32)
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "review marker literal",
			raw:  "packaging; os_name == 'po\\\nsix'\n",
			want: []string{"packaging; os_name == 'posix'"},
		},
		{
			name: "requirement version hash and marker fragments",
			raw: "req\\\nuests==2.32.3\n" +
				"idna==3.\\\n10\n" +
				"typing-extensions==4.12.2 --hash=sha256:" + firstHash + "\\\n" + secondHash + "\n" +
				"urllib3>\\\n=2,<3\n" +
				"charset-normalizer; python_ver\\\nsion >= '3.0'\n" +
				"six; os_name == 'po \\\nsix'\n" +
				"certifi; os_name == 'po\\\n six'\n",
			want: []string{
				"requests==2.32.3",
				"idna==3.10",
				"typing-extensions==4.12.2 --hash=sha256:" + firstHash + secondHash,
				"urllib3>=2,<3",
				"charset-normalizer; python_version >= '3.0'",
				"six; os_name == 'po six'",
				"certifi; os_name == 'po six'",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pyDepsAssertAcceptedSemanticInput(t, tc.raw, tc.want)
		})
	}
}

// #253: coding: and coding= are declarations only within comments. Names
// ending in coding and marker values containing that text remain ordinary
// requirement syntax. Declarations in comments remain forbidden at every
// position, including after an otherwise accepted package-name regression.
func TestPythonDependenciesAcceptCodingPackageNames(t *testing.T) {
	t.Run("package names and marker text", func(t *testing.T) {
		pyDepsAssertAcceptedSemanticInput(t,
			"geocoding==1.0\n"+
				"coding==2.0\n"+
				"decoding==3.0\n"+
				"fileencoding==4.0 # ordinary package comment\n"+
				"transcoding; os_name == 'coding:utf-8'\n"+
				"encoding-tools; os_name == 'coding=utf-8'\n",
			[]string{
				"geocoding==1.0",
				"coding==2.0",
				"decoding==3.0",
				"fileencoding==4.0",
				"transcoding; os_name == 'coding:utf-8'",
				"encoding-tools; os_name == 'coding=utf-8'",
			})
	})
	t.Run("comment declarations remain rejected anywhere", func(t *testing.T) {
		pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
			{"after accepted package", "geocoding==1.0\n# coding: utf-8\n"},
			{"late after accepted packages", "geocoding==1.0\ndecoding==2.0\n\n# coding=utf-8\n"},
			{"trailing accepted package", "geocoding==1.0 # coding: utf-8\n"},
			{"indented comment", "geocoding==1.0\n\t  # -*- coding: utf-8 -*-\n"},
			{"CRLF late comment", "geocoding==1.0\r\n# ordinary\r\n# coding=utf-8\r\n"},
			{"BOM late comment", "\xef\xbb\xbfgeocoding==1.0\n# ordinary\n# coding: utf-8\n"},
		})
	})
}

// Every accepted shape is exercised in each effective-file role and supported
// byte encoding. The expected logical lines are written independently of the
// production parser. Both receipt representations are checked against their
// actual bytes, and rebuilding must reuse the same environment evidence.
func pyDepsAssertAcceptedSemanticInput(t *testing.T, raw string, want []string) {
	t.Helper()
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"}
	for _, filename := range inputs {
		t.Run(filename, func(t *testing.T) {
			var firstCanonical map[string]string
			for _, encoding := range []struct {
				name    string
				bom     string
				newline string
			}{
				{"LF", "", "\n"},
				{"CRLF", "", "\r\n"},
				{"BOM LF", "\xef\xbb\xbf", "\n"},
				{"BOM CRLF", "\xef\xbb\xbf", "\r\n"},
			} {
				t.Run(encoding.name, func(t *testing.T) {
					pyDepsFakeDocker(t)
					root := pyDepsFixture(t)
					files := pyDepsFiles(t, root)
					// Put the regression first, including on line one for the
					// package-name case. Existing includes preserve the closure.
					body := raw + files[filename]
					pyDepsWrite(t, root, filename, encoding.bom+strings.ReplaceAll(body, "\n", encoding.newline))
					before := pyDepsFiles(t, root)
					env := pyDepsBuild(t, root)
					pyDepsAssertRecipeEnvironment(t, root, env, inputs)
					canonical := pyDepsAssertCanonicalStaging(t, root, env, inputs)
					wantRequirements := []string{"packaging>=24,<25"}
					wantConstraints := []string{"packaging==24.2"}
					if filename == "deps/constraints.txt" {
						wantConstraints = append(wantConstraints, want...)
					} else {
						wantRequirements = append(wantRequirements, want...)
					}
					pyDepsAssertCanonicalLines(t, canonical["requirements.txt"], wantRequirements)
					pyDepsAssertCanonicalLines(t, canonical["constraints.txt"], wantConstraints)
					if firstCanonical == nil {
						firstCanonical = canonical
					} else if !reflect.DeepEqual(canonical, firstCanonical) {
						t.Fatalf("BOM or newline style changed accepted semantics: got=%#v want=%#v", canonical, firstCanonical)
					}
					if again := pyDepsBuild(t, root); !reflect.DeepEqual(again, env) || pyDepsBuildCount(t) != 1 {
						t.Fatalf("accepted semantics did not reuse exact evidence: first=%#v again=%#v", env, again)
					}
					if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
						t.Fatal("accepting requirement syntax rewrote application inputs")
					}
				})
			}
		})
	}
}
