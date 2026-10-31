package verify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPythonApplicationExecutionModuleInterleaved(t *testing.T) {
	for _, different := range []bool{false, true} {
		t.Run(map[bool]string{false: "same module", true: "different modules"}[different], func(t *testing.T) {
			driver := `from invariant_observe import module
from tasks import lifecycle as app
from tasks import other
def drive():
    with module(app.advance, fields={'first':'rows[0].value'}) as a:
        with module(other.advance, fields={'first':'rows[0].value'}) as b:
            a.step(app.submit, {'upload':object()}, status='done')
            b.step(other.submit, {'upload':object()}, status='done')
            a.step(app.submit, {'upload':object()}, status='other')
if __name__ == '__main__': drive()
`
			if !different {
				driver = strings.Replace(driver, "from tasks import other", "from tasks import lifecycle as other", 1)
			}
			root, p := pa321Fixture(t, pa321State+pa321Async, driver)
			pa310Write(t, root, "tasks/other.py", pa321State+pa321Async)
			p.Manifest.Components = nil
			p.Manifest.Existing = []string{"tasks"}
			pa310Save(t, p)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			r := pa318Success(t, pa321Context(t), s)
			want := 1
			if different {
				want = 2
			}
			if len(r.Observations) != 5 || r.Observations[0].Instance != 1 || r.Observations[1].Instance != want || r.Observations[3].Instance != want || r.Observations[4].Sequence != 2 {
				t.Fatalf("interleaved identities: %+v", r.Observations)
			}
		})
	}
}

func TestPythonApplicationExecutionModuleFreshness(t *testing.T) {
	for _, mode := range []string{"observer", "staged input", "during execution"} {
		t.Run(mode, func(t *testing.T) {
			_, p := pa321Fixture(t, pa321State+pa321Worker, pa321Driver)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			var command PythonApplicationCommand
			calls := 0
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				command = c
				calls++
				r, err := execute(ctx, c)
				if mode == "during execution" && calls == 2 {
					pa310Write(t, c.Root, "tasks/next.txt", "changed")
				}
				return r, err
			}
			s := pa315Prepare(t, p, opts)
			pa318Success(t, pa321Context(t), s)
			if mode == "observer" {
				pa310Write(t, command.RuntimeDir, "invariant_observe.py", "raise RuntimeError('changed')\n")
			}
			if mode == "staged input" {
				pa310Write(t, command.Root, "tasks/next.txt", "changed")
			}
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if mode != "during execution" && calls != 1 {
				t.Fatal("stale input executed")
			}
		})
	}
}

func TestPythonApplicationExecutionModuleCleanup(t *testing.T) {
	for name, extra := range map[string]string{
		"pending coroutine":   "    asyncio.create_task(asyncio.sleep(30))\n",
		"caught cancellation": "    try:\n        await asyncio.Event().wait()\n    except BaseException:\n        pass\n",
		"canceled target":     "    raise asyncio.CancelledError()\n",
	} {
		t.Run(name, func(t *testing.T) {
			source := pa321State + strings.Replace(pa321Async, "    await advance(request, status=status)\n", "    await advance(request, status=status)\n"+extra, 1)
			driver := strings.Replace(pa321Driver, "timeout=2", "timeout=0.2", 1)
			_, p := pa321Fixture(t, source, driver)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			ctx := pa321Context(t)
			r, err := s.Run(ctx, pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if ctx.Err() != nil {
				t.Fatal("module cleanup exceeded its deadline")
			}
		})
	}
	t.Run("outer cancellation", func(t *testing.T) {
		source := pa321State + strings.Replace(pa321Async, "    await advance(request, status=status)", "    await asyncio.Event().wait()", 1)
		_, p := pa321Fixture(t, source, pa321Driver)
		s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		r, err := s.Run(ctx, pa318Driver(t, s).Key)
		pa318Reject(t, r, err)
		if ctx.Err() == nil {
			t.Fatal("did not reach cancellation")
		}
	})
}

func TestPythonApplicationExecutionModuleIdentityAndWorkers(t *testing.T) {
	for name, source := range map[string]string{
		"cloned registration": strings.Replace(pa321State+pa321Worker, "    return fn", "    import types\n    return types.FunctionType(fn.__code__, fn.__globals__, fn.__name__, fn.__defaults__)", 1),
		"cloned target":       pa321State + pa321Worker + "\nimport types\nadvance = types.FunctionType(advance.__code__, globals())\n",
		"worker failure":      strings.Replace(pa321State+pa321Worker, "    worker.start()", "    worker.start()\n    threading.Thread(target=fail).start()", 1) + "\ndef fail():\n    raise RuntimeError('worker failed')\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, p := pa321Fixture(t, source, pa321Driver)
			s := pa315Prepare(t, p, pa318Options(t, false, new(int)))
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
		})
	}
}

// Re-sign mutations as a trusted transport test, so these exercise Go's
// independent schema checks rather than stopping at HMAC authentication.
func TestPythonApplicationExecutionModuleHostileProtocol(t *testing.T) {
	mutations := map[string]func(map[string]any){
		"unknown form": func(r map[string]any) {
			r["form"] = "future"
		},
		"missing form": func(r map[string]any) {
			delete(r, "form")
		},
		"null form": func(r map[string]any) {
			r["form"] = nil
		},
		"bad path": func(r map[string]any) {
			r["readback"].(map[string]any)["status"] = "tasks[-1].status"
		},
		"changed form": func(r map[string]any) {
			if r["kind"] == "transition" {
				delete(r, "form")
			}
		},
		"wrong identity": func(r map[string]any) {
			if r["kind"] == "transition" {
				r["instance"] = 2
			}
		},
		"broken continuity": func(r map[string]any) {
			if r["kind"] == "transition" {
				r["before"].(map[string]any)["status"] = []any{"forged"}
			}
		},
		"overflow": func(r map[string]any) {
			if r["kind"] == "initial" {
				r["state"].(map[string]any)["details"] = json.Number("1e999")
			}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			_, p := pa321Fixture(t, pa321State+pa321Async, pa321Driver)
			opts := pa318Options(t, false, new(int))
			execute := opts.Execute
			opts.Execute = func(ctx context.Context, c PythonApplicationCommand) (PythonApplicationProcess, error) {
				secret, err := os.ReadFile(filepath.Join(c.OutputDir, "authentication.key"))
				if err != nil {
					t.Fatal(err)
				}
				process, err := execute(ctx, c)
				if err != nil {
					return process, err
				}
				var stream bytes.Buffer
				for _, line := range bytes.Split(bytes.TrimSpace(process.Events), []byte("\n")) {
					var row map[string]any
					d := json.NewDecoder(bytes.NewReader(line))
					d.UseNumber()
					if e := d.Decode(&row); e != nil {
						t.Fatal(e)
					}
					if row["type"] == "authentication" {
						continue
					}
					if row["type"] == "observation" {
						mutate(row)
					}
					b, e := json.Marshal(row)
					if e != nil {
						t.Fatal(e)
					}
					stream.Write(b)
					stream.WriteByte('\n')
				}
				h := hmac.New(sha256.New, secret)
				h.Write(stream.Bytes())
				seal, _ := json.Marshal(map[string]string{"type": "authentication", "hmac_sha256": hex.EncodeToString(h.Sum(nil))})
				stream.Write(seal)
				stream.WriteByte('\n')
				process.Events = stream.Bytes()
				return process, nil
			}
			s := pa315Prepare(t, p, opts)
			r, err := s.Run(pa321Context(t), pa318Driver(t, s).Key)
			pa318Reject(t, r, err)
			if err == nil || strings.Contains(err.Error(), "authentication") {
				t.Fatalf("did not reach schema validation: %v", err)
			}
		})
	}
}

func TestPythonApplicationModuleModelValues(t *testing.T) {
	for _, value := range []any{nil, json.Number("1.5"), map[string]any{"$seq": []any{1}}, map[string]any{"bad.key": true}, map[string]any{"$mv": "x"}} {
		if _, err := pythonApplicationModelValue(value); err == nil {
			t.Fatalf("accepted unsupported module value: %#v", value)
		}
	}
	raw := map[string]any{"items": []any{json.Number("9007199254740993"), map[string]any{"nested": []any{}}}}
	before := pa321JSON(t, raw)
	v, err := pythonApplicationModelValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pa321JSON(t, raw) != before || pa321JSON(t, v) != `{"items":{"$seq":[9007199254740993,{"nested":{"$seq":[]}}]}}` {
		t.Fatalf("conversion mutated or lost values: %#v", v)
	}
}
