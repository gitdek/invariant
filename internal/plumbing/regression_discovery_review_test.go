package plumbing

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRegressionDiscoveryReview654(t *testing.T) {
	for _, name := range []string{"pytest.ini", ".pytest.ini", "tox.ini", "setup.cfg", "pyproject.toml", "pytest.toml", ".pytest.toml"} {
		t.Run("nested pytest/"+name, func(t *testing.T) {
			text := "[pytest]\npython_files = test_contract.py\n"
			if name == "setup.cfg" {
				text = "[tool:pytest]\npython_files = test_contract.py\n"
			} else if name == "pyproject.toml" {
				text = "[tool.pytest.ini_options]\npython_files = ['test_contract.py']\n"
			}
			checkDiscoveryReview654(t, "python", []string{"pytest", "tests"}, map[string]string{"app/tests/" + name: text}, "app/tests/test_regression.py", false)
		})
	}
	for _, tc := range []struct {
		name, language, config, text, file string
		argv                               []string
		allowed                            bool
	}{
		{"pytest root restriction", "python", "app/pytest.ini", "[pytest]\npython_files = test_contract.py\n", "app/tests/test_regression.py", []string{"pytest", "tests"}, false},
		{"pytest root inclusion", "python", "app/pytest.ini", "[pytest]\npython_files = test_*.py\n", "app/tests/test_regression.py", []string{"pytest", "tests"}, true},
		{"jest relative pattern", "typescript", "app/jest.config.json", `{"testMatch":["<rootDir>/contract.test.js","tests/**/*.test.js"]}`, "app/tests/regression.test.js", []string{"jest"}, false},
		{"jest relative basename", "typescript", "app/jest.config.json", `{"testMatch":["regression.test.js"]}`, "app/regression.test.js", []string{"jest"}, false},
		{"jest anchored pattern", "typescript", "app/jest.config.json", `{"testMatch":["<rootDir>/contract.test.js","<rootDir>/tests/**/*.test.js"]}`, "app/tests/regression.test.js", []string{"jest"}, true},
		{"jest globstar pattern", "typescript", "app/jest.config.json", `{"testMatch":["**/tests/**/*.test.js"]}`, "app/tests/regression.test.js", []string{"jest"}, true},
		{"jest anchor excludes sibling", "typescript", "app/jest.config.json", `{"testMatch":["<rootDir>/tests/**/*.test.js"]}`, "app/src/tests/regression.test.js", []string{"jest"}, false},
		{"vitest rootDir token", "typescript", "app/vitest.config.mjs", `export default {test: {include: ['contract.test.js', '<rootDir>/tests/**/*.test.js']}};`, "app/tests/regression.test.js", []string{"vitest", "run"}, false},
		{"vitest relative pattern", "typescript", "app/vitest.config.mjs", `export default {test: {include: ['contract.test.js', 'tests/**/*.test.js']}};`, "app/tests/regression.test.js", []string{"vitest", "run"}, true},
		{"vitest rootDir exclusion", "typescript", "app/vitest.config.mjs", `export default {test: {exclude: ['<rootDir>/tests/**/*.test.js']}};`, "app/tests/regression.test.js", []string{"vitest", "run"}, false},
		{"jest inline babel", "typescript", "app/package.json", `{"babel":{"plugins":["./rewrite.cjs"]}}`, "app/tests/regression.test.js", []string{"jest"}, false},
		{"jest inline babel beside jest", "typescript", "app/package.json", `{"jest":{"testMatch":["<rootDir>/tests/**/*.test.js"]},"babel":{"plugins":["./rewrite.cjs"]}}`, "app/tests/regression.test.js", []string{"jest"}, false},
		{"jest nested inline babel", "typescript", "app/tests/package.json", `{"babel":{"plugins":["./rewrite.cjs"]}}`, "app/tests/regression.test.js", []string{"jest"}, false},
		{"jest ordinary package", "typescript", "app/package.json", `{"name":"fixture","jest":{"testMatch":["<rootDir>/tests/**/*.test.js"]}}`, "app/tests/regression.test.js", []string{"jest"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkDiscoveryReview654(t, tc.language, tc.argv, map[string]string{tc.config: tc.text}, tc.file, tc.allowed)
		})
	}
}

func checkDiscoveryReview654(t *testing.T, language string, argv []string, files map[string]string, file string, allowed bool) {
	t.Helper()
	root, plan := regressionPolicyFixture(t, language, argv, files)
	scope, err := checkoutRegressionScope(t.Context(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.Files([]string{file}); slices.Contains(got, file) != allowed {
		t.Fatalf("discovery admitted %v, want allowed=%v", got, allowed)
	}
	candidate, staged := t.TempDir(), t.TempDir()
	if err := CopyCheckout(t.Context(), root, candidate); err != nil {
		t.Fatal(err)
	}
	write468(t, candidate, file, "regression bytes\n")
	// Removing the captured configuration cannot reopen implicit permission.
	for name := range files {
		if err := os.Remove(filepath.Join(candidate, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Stage(t.Context(), root, candidate, plan, staged); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(staged, file))
	if allowed {
		if err != nil || string(data) != "regression bytes\n" {
			t.Fatalf("staging lost admitted regression: %q, %v", data, err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("staging admitted unsupported regression: %q, %v", data, err)
	}
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(staged, name))
		if err != nil || string(data) != want {
			t.Fatalf("staging changed captured %s: %q, %v", name, data, err)
		}
	}
}

// These opt-ins use the existing pinned local images, with real frameworks and
// no installation, downloads or substituted command results. Each suite first
// hides a failing regression, then its supported configuration exposes it.
func TestPytestOfflineRegressionDiscovery654(t *testing.T) {
	checkDiscoveryReview654(t, "python", []string{"pytest", "tests"}, map[string]string{
		"app/tests/pytest.ini": "[pytest]\npython_files = test_contract.py\n",
	}, "app/tests/test_regression.py", false)
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	out, err := dependencyDocker(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--platform", platform, "--cap-drop", "ALL", "--entrypoint", "python3", image, "-I", "-c", pytestDiscoveryReview654)
	if err != nil || !strings.Contains(string(out), "discovery654 verified") {
		t.Fatalf("real pytest discovery: %v\n%s", err, out)
	}
}

const pytestDiscoveryReview654 = `import os, pathlib, subprocess, sys, tempfile
root = pathlib.Path(tempfile.mkdtemp()) / 'app'
(root / 'tests').mkdir(parents=True)
config = root / 'tests/pytest.ini'
config.write_text('[pytest]\npython_files = test_contract.py\n')
(root / 'tests/test_contract.py').write_text('def test_contract():\n    assert True\n')
(root / 'tests/test_regression.py').write_text('def test_regression():\n    assert False\n')
env = dict(os.environ, PYTEST_DISABLE_PLUGIN_AUTOLOAD='1')
def run():
    p = subprocess.run([sys.executable, '-m', 'pytest', '-q', 'tests'], cwd=root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
    print(p.stdout)
    return p
p = run()
assert p.returncode == 0 and '1 passed' in p.stdout, p.stdout
config.unlink()
(root / 'pytest.ini').write_text('[pytest]\npython_files = test_*.py\n')
p = run()
assert p.returncode == 1 and '1 failed, 1 passed' in p.stdout, p.stdout
print('discovery654 verified')
`

func TestNodeOfflineRegressionDiscovery654(t *testing.T) {
	checkDiscoveryReview654(t, "typescript", []string{"jest"}, map[string]string{
		"app/jest.config.json": `{"testMatch":["<rootDir>/contract.test.js","tests/**/*.test.js"]}`,
	}, "app/tests/regression.test.js", false)
	checkDiscoveryReview654(t, "typescript", []string{"vitest", "run"}, map[string]string{
		"app/vitest.config.mjs": `export default {test: {include: ['contract.test.js', '<rootDir>/tests/**/*.test.js']}};`,
	}, "app/tests/regression.test.js", false)
	checkDiscoveryReview654(t, "typescript", []string{"jest"}, map[string]string{
		"app/package.json": `{"babel":{"plugins":["./rewrite.cjs"]}}`,
	}, "app/tests/regression.test.js", false)
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"jest", "vitest", "babel"} {
		t.Run(framework, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			out, err := dependencyDocker(ctx, "run", "--rm", "--pull", "never", "--network", "none", "--platform", tools.platform, "--cap-drop", "ALL", "--entrypoint", "node", tools.image, "-e", nodeDiscoveryReview654, framework)
			var results []struct {
				Status int `json:"status"`
				Tests  int `json:"tests"`
				Failed int `json:"failed"`
			}
			if err != nil || json.Unmarshal(out, &results) != nil || len(results) != 2 {
				t.Fatalf("real %s discovery: %v\n%s", framework, err, out)
			}
			first := 1
			if framework == "babel" {
				first = 2
			}
			if results[0].Status != 0 || results[0].Tests != first || results[0].Failed != 0 || results[1].Status != 1 || results[1].Tests != 2 || results[1].Failed != 1 {
				t.Fatalf("real %s did not hide then expose the failing regression: %+v", framework, results)
			}
		})
	}
}

const nodeDiscoveryReview654 = `const fs = require('node:fs'), os = require('node:os'), path = require('node:path');
const cp = require('node:child_process'), assert = require('node:assert/strict');
const framework = process.argv[1];
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'discovery654-'));
process.chdir(root);
fs.mkdirSync('tests');
fs.symlinkSync('/opt/invariant-633/node/node_modules', 'node_modules', 'dir');
fs.writeFileSync('package.json', '{}');
const vitest = framework === 'vitest';
const imports = vitest ? "import {test,expect} from 'vitest';\n" : '';
fs.writeFileSync('contract.test.js', imports + "test('contract',()=>expect(1).toBe(1));\n");
fs.writeFileSync('tests/regression.test.js', imports + "test('regression',()=>expect(1).toBe(654));\n");
function config(expose) {
    if (framework === 'jest') {
        fs.writeFileSync('jest.config.json', JSON.stringify({testMatch: ['<rootDir>/contract.test.js', (expose ? '<rootDir>/' : '') + 'tests/**/*.test.js']}));
    } else if (vitest) {
        fs.writeFileSync('vitest.config.mjs', 'export default ' + JSON.stringify({test: {include: ['contract.test.js', (expose ? '' : '<rootDir>/') + 'tests/**/*.test.js']}}));
    } else {
        fs.writeFileSync('package.json', JSON.stringify(expose ? {} : {babel: {plugins: ['./rewrite.cjs']}}));
        fs.writeFileSync('rewrite.cjs', "module.exports=()=>({visitor:{NumericLiteral(p){if(p.node.value===654){p.node.value=1;require('node:fs').writeFileSync('babel-ran','yes');}}}});\n");
    }
}
const results = [];
for (const expose of [false, true]) {
    config(expose);
    const args = vitest ? ['node_modules/vitest/vitest.mjs', 'run', '--reporter=json', '--pool=forks', '--minWorkers=1', '--maxWorkers=1'] : ['node_modules/jest/bin/jest.js', '--runInBand', '--json', '--no-cache'];
    const result = cp.spawnSync(process.execPath, args, {cwd: root, encoding: 'utf8', timeout: 20000});
    assert.equal(result.error, undefined, String(result.error));
    assert.equal(result.signal, null, result.stderr);
    let report;
    try { report = JSON.parse(result.stdout); }
    catch (e) { throw new Error(result.stdout + '\n' + result.stderr); }
    results.push({status: result.status, tests: report.numTotalTests, failed: report.numFailedTests});
    if (framework === 'babel' && !expose) assert.equal(fs.readFileSync('babel-ran', 'utf8'), 'yes');
}
console.log(JSON.stringify(results));
`
