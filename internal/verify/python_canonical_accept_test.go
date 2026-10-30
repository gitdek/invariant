package verify

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPythonDependenciesRejectBackslashExploit(t *testing.T) {
	// Exact #246 shape: two literal trailing backslashes. pip removes the
	// whole run while the old validator removed one, so the following option
	// escaped validation during a network-enabled installation.
	const review = "packaging; python_version >= '3.0' " + "\\\\" + "\n--no-binary :all:\n"
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"review source builds", review},
		{"review index redirection", strings.ReplaceAll(review, "--no-binary :all:", "--index-url https://example.invalid/simple")},
	})
}

func TestPythonDependenciesRejectUnsupportedBackslashes(t *testing.T) {
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"double trailing", "packaging==24.2 " + strings.Repeat("\\", 2) + "\n"},
		{"triple trailing", "packaging==24.2 " + strings.Repeat("\\", 3) + "\n--hash=sha256:" + strings.Repeat("a", 64) + "\n"},
		{"long trailing run", "packaging==24.2 " + strings.Repeat("\\", 8) + "\n"},
		{"inside package", "pack\\aging==24.2\n"},
		{"inside marker value", "packaging; os_name == 'po\\six'\n"},
		{"escaped marker quote", "packaging; os_name == 'po\\'six'\n"},
		{"inside include path", "-r deps\\application.txt\n"},
		{"inside hash", "packaging==24.2 --hash=sha256:" + strings.Repeat("a", 32) + "\\" + strings.Repeat("b", 32) + "\n"},
		{"inside comment", "# comment with a \\ backslash\npackaging==24.2\n"},
		{"run in comment", "# comment " + strings.Repeat("\\", 2) + "\npackaging==24.2\n"},
		{"before inline comment", "packaging==24.2 \\ # comment\n"},
		{"before trailing space", "packaging==24.2 \\ \n--hash=sha256:" + strings.Repeat("a", 64) + "\n"},
		{"before trailing tab", "packaging==24.2 \\\t\n"},
		{"dangling without LF", "packaging==24.2 \\"},
		{"dangling with LF", "packaging==24.2 \\\n"},
	})
}

func TestPythonDependenciesRejectUnsupportedGrammar(t *testing.T) {
	pyDepsRejectInvalidInputs(t, []pyDepsInvalidInputCase{
		{"two requirements on one line", "packaging==24.2 idna==3.10\n"},
		{"empty extra", "packaging[foo,,bar]==24.2\n"},
		{"unterminated extras", "packaging[foo==24.2\n"},
		{"missing version", "packaging==\n"},
		{"unparsed requirement suffix", "packaging==24.2 garbage\n"},
		{"unknown marker variable", "packaging; mystery_platform == 'linux'\n"},
		{"invalid marker operator", "packaging; python_version = '3.0'\n"},
		{"unterminated marker literal", "packaging; python_version >= '3.0\n"},
		{"unbalanced marker parentheses", "packaging; (python_version >= '3.0'\n"},
		{"incomplete marker conjunction", "packaging; python_version >= '3.0' and\n"},
		{"unparsed marker suffix", "packaging; python_version >= '3.0' garbage\n"},
		{"second marker separator", "packaging; python_version >= '3.0'; os_name == 'posix'\n"},
		{"source option after marker", "packaging; python_version >= '3.0' --no-binary :all:\n"},
		{"index option after marker", "packaging; python_version >= '3.0' --index-url https://example.invalid/simple\n"},
		{"continued source option", "packaging; python_version >= '3.0' \\\n    --no-binary :all:\n"},
		{"continued index option", "packaging; python_version >= '3.0' \\\n    --index-url https://example.invalid/simple\n"},
		// pip's option splitter can recognize a space-delimited option even
		// inside a quoted PEP 508 marker literal. Such atoms are outside the
		// supported grammar; copying them into canonical files is not safe.
		{"source option inside marker literal", "packaging; python_version >= '3.0 --no-binary :all:'\n"},
		{"index option inside marker literal", "packaging; python_version >= '3.0 --index-url https://example.invalid/simple'\n"},
		{"source option as marker literal", "packaging; python_version == '--no-binary :all:'\n"},
		{"index option as marker literal", "packaging; python_version == '--index-url https://example.invalid/simple'\n"},
		{"include option inside marker literal", "packaging; os_name == 'posix -r deps/application.txt'\n"},
		{"hash option inside marker literal", "packaging; os_name == 'posix --hash=sha256:" + strings.Repeat("a", 64) + "'\n"},
		{"hash option as marker literal", "packaging; os_name == '--hash=sha256:" + strings.Repeat("a", 64) + "'\n"},
		// A literal # has different comment behavior across pip parsing
		// stages. Marker strings containing it are unsupported; ordinary
		// comments remain supported and are removed during canonicalization.
		{"comment character inside marker literal", "packaging; os_name == 'posix # ignored'\n"},
		{"comment character as marker literal", "packaging; os_name == '#'\n"},
		{"build setting after requirement", "packaging==24.2 --config-settings=backend.key=value\n"},
		{"include followed by option", "-r deps/application.txt --no-binary :all:\n"},
		{"constraint followed by extra token", "-c deps/constraints.txt garbage\n"},
		{"standalone hash", "--hash=sha256:" + strings.Repeat("a", 64) + "\n"},
		{"unsupported hash algorithm", "packaging==24.2 --hash=md5:" + strings.Repeat("a", 32) + "\n"},
		{"short SHA256", "packaging==24.2 --hash=sha256:abcd\n"},
		{"nonhex SHA256", "packaging==24.2 --hash=sha256:" + strings.Repeat("g", 64) + "\n"},
		{"missing hash value", "packaging==24.2 --hash\n"},
		{"unknown inline option", "packaging==24.2 --unknown-option=value\n"},
	})
}

func TestPythonDependenciesCanonicalizeAcceptedRequirements(t *testing.T) {
	t.Run("recursive closure and single continuations", func(t *testing.T) {
		pyDepsFakeDocker(t)
		root := pyDepsFixture(t)
		firstHash, secondHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
		pyDepsWrite(t, root, "requirements.txt", "\xef\xbb\xbf# café root requirements\r\n--requirement \\\r\n    deps/application.txt\r\n--constraint deps/constraints.txt\r\n-r deps/shared.txt\r\n")
		pyDepsWrite(t, root, "deps/application.txt", "# requirement include\npackaging==24.2 # requested package\n-rnested/extras.txt\n")
		pyDepsWrite(t, root, "deps/nested/extras.txt", "requests[security,socks]==2.32.3 ; python_version < '0' and (os_name == 'posix' or sys_platform == 'linux') \\\n    --hash=sha256:"+firstHash+" \\\n    --hash=sha256:"+secondHash+" # accepted hashes\n")
		pyDepsWrite(t, root, "deps/constraints.txt", "packaging==24.2\n-c nested/constraints.txt\n--constraint=shared.txt\n")
		pyDepsWrite(t, root, "deps/nested/constraints.txt", "# nested constraint\nrequests<3 ; \\\n    python_version >= '3.0'\n")
		pyDepsWrite(t, root, "deps/shared.txt", "idna==3.10 ; python_version < '0'\n")
		inputs := []string{"requirements.txt", "deps/application.txt", "deps/nested/extras.txt", "deps/constraints.txt", "deps/nested/constraints.txt", "deps/shared.txt"}
		before := pyDepsFiles(t, root)
		env := pyDepsBuild(t, root)
		pyDepsAssertRecipeEnvironment(t, root, env, inputs)
		canonical := pyDepsAssertCanonicalStaging(t, root, env, inputs)
		// The shared file is hashed once in Inputs and visited in both roles:
		// its entry must be present in each canonical file. Synthetic hashes
		// exercise serialization here; real recipe execution has its own test.
		pyDepsAssertCanonicalLines(t, canonical["requirements.txt"], []string{
			"packaging==24.2",
			"requests[security,socks]==2.32.3; python_version < '0' and (os_name == 'posix' or sys_platform == 'linux') --hash=sha256:" + firstHash + " --hash=sha256:" + secondHash,
			"idna==3.10; python_version < '0'",
		})
		pyDepsAssertCanonicalLines(t, canonical["constraints.txt"], []string{
			"packaging==24.2",
			"requests<3; python_version >= '3.0'",
			"idna==3.10; python_version < '0'",
		})
		if second := pyDepsBuild(t, root); !reflect.DeepEqual(env, second) || pyDepsBuildCount(t) != 1 {
			t.Fatalf("canonical dependency environment did not reuse exact evidence: first=%#v second=%#v", env, second)
		}
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("canonicalization rewrote the application's requirement files or other source")
		}
	})
	t.Run("explicit empty constraints file", func(t *testing.T) {
		pyDepsFakeDocker(t)
		root := pyDepsFixture(t)
		pyDepsWrite(t, root, "requirements.txt", "# only a root requirement\npackaging==24.2\n")
		before := pyDepsFiles(t, root)
		env := pyDepsBuild(t, root)
		canonical := pyDepsAssertCanonicalStaging(t, root, env, []string{"requirements.txt"})
		pyDepsAssertCanonicalLines(t, canonical["requirements.txt"], []string{"packaging==24.2"})
		if canonical["constraints.txt"] != "" {
			t.Fatalf("no constraints must produce an empty canonical constraints file, got %q", canonical["constraints.txt"])
		}
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("canonicalization rewrote an application with no constraints")
		}
	})
}

func TestPythonDependenciesCanonicalCacheTracksBothRepresentations(t *testing.T) {
	pyDepsFakeDocker(t)
	root := pyDepsFixture(t)
	inputs := []string{"requirements.txt", "deps/application.txt", "deps/constraints.txt"}
	original := pyDepsFiles(t, root)
	first := pyDepsBuild(t, root)
	canonical := pyDepsAssertCanonicalStaging(t, root, first, inputs)
	builds := 1
	// Source bytes remain provenance even when canonicalization removes a
	// comment. Keying reuse only on canonical bytes would miss each change.
	for _, filename := range inputs {
		pyDepsWrite(t, root, filename, original[filename]+"# canonical-inert provenance change\n")
		before := pyDepsFiles(t, root)
		changed := pyDepsBuild(t, root)
		got := pyDepsAssertCanonicalStaging(t, root, changed, inputs)
		builds++
		if changed.Recipe == first.Recipe || reflect.DeepEqual(changed.Inputs, first.Inputs) || pyDepsBuildCount(t) != builds {
			t.Fatalf("changing raw %s reused the old recipe or its provenance: first=%#v changed=%#v builds=%d", filename, first, changed, pyDepsBuildCount(t))
		}
		if !reflect.DeepEqual(got, canonical) || !reflect.DeepEqual(changed.CanonicalInputs, first.CanonicalInputs) {
			t.Fatalf("a comment in %s changed canonical installation bytes or hashes: got=%#v want=%#v", filename, got, canonical)
		}
		if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
			t.Fatal("building changed original dependency bytes")
		}
		pyDepsWrite(t, root, filename, original[filename])
		if restored := pyDepsBuild(t, root); !reflect.DeepEqual(restored, first) || pyDepsBuildCount(t) != builds {
			t.Fatalf("restoring the original bytes did not reuse the original environment: %#v", restored)
		}
	}
	// An effective constraint change updates canonical bytes and their hashes
	// as well as original provenance, recipe identity and installed evidence.
	pyDepsWrite(t, root, "deps/constraints.txt", "packaging==24.1\n")
	before := pyDepsFiles(t, root)
	changed := pyDepsBuild(t, root)
	got := pyDepsAssertCanonicalStaging(t, root, changed, inputs)
	builds++
	if changed.Recipe == first.Recipe || reflect.DeepEqual(changed.CanonicalInputs, first.CanonicalInputs) || pyDepsBuildCount(t) != builds {
		t.Fatalf("effective constraints did not change canonical provenance and the cached recipe: first=%#v changed=%#v", first, changed)
	}
	if got["requirements.txt"] != canonical["requirements.txt"] || got["constraints.txt"] == canonical["constraints.txt"] {
		t.Fatalf("constraint change affected the wrong canonical file: before=%#v after=%#v", canonical, got)
	}
	pyDepsAssertCanonicalLines(t, got["constraints.txt"], []string{"packaging==24.1"})
	pyDepsAssertEnvironment(t, root, changed, "24.1")
	if second := pyDepsBuild(t, root); !reflect.DeepEqual(second, changed) || pyDepsBuildCount(t) != builds {
		t.Fatalf("changed canonical recipe did not reuse its evidence: %#v", second)
	}
	if !reflect.DeepEqual(before, pyDepsFiles(t, root)) {
		t.Fatal("canonical cache handling rewrote application files")
	}
}

// Compare a multiset of fully parsed fixture requirements without dictating
// quote style, harmless whitespace or line order. No comments, continuations,
// includes or pip options other than complete SHA256 hash tokens can survive.
func pyDepsAssertCanonicalLines(t *testing.T, body string, want []string) {
	t.Helper()
	if strings.ContainsAny(body, "\\#\r") || strings.Contains(body, "\xef\xbb\xbf") || (body != "" && !strings.HasSuffix(body, "\n")) {
		t.Fatalf("canonical file contains source syntax or lacks final LF: %q", body)
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
		if line == "" {
			if body != "" {
				t.Fatal("canonical file contains an empty requirement line")
			}
			continue
		}
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "-") && (!strings.HasPrefix(token, "--hash=") || !pyDepsDigest.MatchString(strings.TrimPrefix(token, "--hash="))) {
				t.Fatalf("canonical file retained an include or installer option: %q", line)
			}
		}
		got = append(got, pyDepsComparableRequirement(t, line))
	}
	var expected []string
	for _, line := range want {
		expected = append(expected, pyDepsComparableRequirement(t, line))
	}
	sort.Strings(got)
	sort.Strings(expected)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("canonical requirements lost or changed accepted semantics:\n got %q\nwant %q\nraw canonical bytes %q", got, expected, body)
	}
}

func pyDepsComparableRequirement(t *testing.T, line string) string {
	t.Helper()
	var normalized strings.Builder
	var quote rune
	for _, r := range line {
		if quote != 0 {
			if r == quote {
				quote = 0
				normalized.WriteRune('\'')
			} else {
				normalized.WriteRune(r)
			}
		} else if r == '\'' || r == '"' {
			quote = r
			normalized.WriteRune('\'')
		} else if r != ' ' && r != '\t' {
			normalized.WriteRune(r)
		}
	}
	if quote != 0 {
		t.Fatalf("canonical requirement has an unterminated marker literal: %q", line)
	}
	return normalized.String()
}
