package plumbing

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This uses the locked runner's three-argument scheduling contract without
// making installed frameworks a prerequisite of the ordinary Go suite.
func TestNodeJestSchedulerKeepsSingleFileOutsideController(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("local Node is unavailable; pinned-image worker coverage is opt-in")
	}
	root := t.TempDir()
	files := map[string][]byte{
		"node_runner_common.cjs":                nodeRunnerCommon,
		"jest_scheduler.cjs":                    []byte(jestScheduler),
		"package.json":                          []byte(`{}`),
		"package-lock.json":                     []byte(`{"packages":{"node_modules/jest-runner":{"version":"29.7.0","integrity":"fixture"}}}`),
		"node_modules/jest-runner/package.json": []byte(`{"name":"jest-runner","version":"29.7.0","main":"index.cjs"}`),
		"node_modules/jest-runner/index.cjs": []byte(`exports.default=class {
  runTests(tests,watcher,options) {
    require('node:assert/strict').deepEqual([tests,watcher,options],[['selected'],{fixture:true},{serial:false,retained:true}]);
    return 'isolated';
  }
};`),
	}
	for name, data := range files {
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-e", `const assert=require('node:assert/strict');
const common=require('./node_runner_common.cjs');
assert(Object.isFrozen(common));
assert.equal(new (require('./jest_scheduler.cjs'))().runTests(['selected'],{fixture:true},{serial:true,retained:true}),'isolated');`)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("single-file scheduling lost isolation: %v %s", err, out)
	}
}

func TestNodeOfflineObserverAuthority(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		for _, attack := range []string{"array binding", "object binding", "config import", "config getter", "config call"} {
			t.Run(framework+"/"+attack, func(t *testing.T) {
				source := nodeImports633(framework) + "import {future} from './product';test('selected',()=>expect(future()).toBe(7));\n"
				extra := map[string]string{}
				// The old probe evaluated this default initializer in the
				// authenticated parent, where it could replace observer and
				// requireLocked before the runner constructed its lifecycle.
				hijack := `function hijack(): any {
console.log('AUTHORITY_HIJACK633');
for (const loaded of Object.values(require.cache)) {
  if (loaded?.filename.endsWith('/node_runner_common.cjs')) {
    const original=loaded.exports.requireLocked;
    loaded.exports.observer=(session:any)=>{
      session.emit('collection',{IDs:[session.input.Binding.Selector]});
      session.emit('body_start');session.emit('body_end',{Outcome:'passed'});
      session.emit('hooks',{Outcome:'passed'});session.emit('suite_end',{Outcome:'passed'});
      return {done:true,outcome:'passed'};
    };
    loaded.exports.requireLocked=(name:string)=>name!=='jest'?original(name):{runCLI:async()=>({results:{
      success:true,testResults:[{testFilePath:process.cwd()+'/future.test.ts',testResults:[{fullName:'selected',status:'passed'}]}]
    }})};
  }
}
return '';
}
`
				switch attack {
				case "array binding":
					extra["web/product.ts"] = hijack + "export const [value = hijack()] = '';\n"
				case "object binding":
					extra["web/product.ts"] = hijack + "export const {missing: value = hijack()}: any = '';\n"
				case "config import":
					extra["web/"+framework+".config.cjs"] = "console.log('AUTHORITY_HIJACK633');require('./config-payload.cjs');module.exports={};\n"
					extra["web/config-payload.cjs"] = "for(const m of Object.values(require.cache)){if(m.filename.endsWith('/node_runner_common.cjs'))m.exports.observer=()=>({done:true,outcome:'passed'});}\n"
				case "config getter":
					field := "test"
					if framework == "jest" {
						field = "transform"
					}
					extra["web/"+framework+".config.cjs"] = "module.exports={get " + field + "(){console.log('AUTHORITY_HIJACK633');return {};}};\n"
				case "config call":
					extra["web/"+framework+".config.cjs"] = "module.exports=(()=>{console.log('AUTHORITY_HIJACK633');return {};})();\n"
				}
				f := fixture633(t, tools, []nativeCase633{{framework, "web", "future.test.ts", "selected", source}}, extra, nil)
				check633(t, f, false)
				if r, _ := f.sandbox.Run(t.Context(), f.candidate, f.plan); r.Passed() {
					t.Fatal("candidate execution forged authenticated completion")
				}
				if len(f.runner.streams) == 0 {
					t.Fatal("attack did not reach the Node observer")
				}
				for _, out := range f.runner.streams {
					if bytes.Contains(out.Stdout, []byte("AUTHORITY_HIJACK633")) {
						t.Fatal("candidate declarations or configuration executed during analysis")
					}
				}
			})
		}
	}
}

func TestNodeOfflineFrameworkGlobals(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			extra := map[string]string{}
			if framework == "vitest" {
				extra["web/vitest.config.mjs"] = "export default {test:{globals:true}};\n"
			} else {
				// @types/jest is an explicit locked prerequisite of this
				// opt-in regression, not a dependency fetched by the test.
				extra["web/jest.config.cjs"] = "module.exports={testEnvironment:'node',transform:{'^.+\\\\.tsx?$':['ts-jest',{tsconfig:{target:'ES2022',module:'commonjs',types:['node','jest']},diagnostics:true}]}};\n"
			}
			source := "import {value} from './product';describe('outer',()=>test('global',()=>expect(value).toBe(7)));\n"
			f := fixture633(t, tools, []nativeCase633{{framework, "web", "global.test.ts", "outer global", source}}, extra, nil)
			c := check633(t, f, true)
			if result617(t, c.Run, f.plan.Tests[0]).Kind != "test" {
				t.Fatal("global declarations did not reach an actual assertion")
			}
			fix633(t, f)
			pass633(t, f)
			// Globals are supported at runtime. Compiler-only attribution
			// still requires verified named framework imports.
			missing := "import {future} from './product';test('global',()=>expect(future()).toBe(7));\n"
			g := fixture633(t, tools, []nativeCase633{{framework, "web", "global.test.ts", "global", missing}}, extra, nil)
			check633(t, g, false)
		})
	}
}

func TestNodeOfflineRuntimeCannotEnterController(t *testing.T) {
	tools, enabled := nodeOffline633(t)
	if !enabled {
		return
	}
	for _, framework := range []string{"vitest", "jest"} {
		t.Run(framework, func(t *testing.T) {
			source := nodeImports633(framework) + "import {value} from './product';test('selected',()=>expect(value).toBe(7));\n"
			boundary := "import {isMainThread} from 'node:worker_threads';if(!isMainThread || /(?:jest_runner.cjs|vitest_runner.mjs)$/.test(process.argv[1]))throw new Error('candidate reached controller');\n"
			extra := map[string]string{"web/product.ts": boundary + "export const value=0;\n"}
			f := fixture633(t, tools, []nativeCase633{{framework, "web", "ready.test.ts", "selected", source}}, extra, nil)
			check633(t, f, true)
			write468(t, f.candidate, "web/product.ts", boundary+"export const value=7;\n")
			pass633(t, f)
		})
	}
}
