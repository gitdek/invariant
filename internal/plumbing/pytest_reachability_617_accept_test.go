package plumbing

import "testing"

func TestPytestOfflineMissingAPIReachability617(t *testing.T) {
	image, platform, enabled := offline617(t)
	if !enabled {
		return
	}
	// An ordinary direct use still qualifies, and implementing the proposed
	// API must turn the unchanged acceptance into a normal runtime pass.
	t.Run("direct use", func(t *testing.T) {
		source := "from product import future\ndef test_ready():\n    assert future() == 7\n"
		f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", "test_ready", source}}, nil, nil)
		c := check617(t, f, true)
		v := result617(t, c.Run, f.plan.Tests[0])
		if v.Kind != "compile" || v.Evidence == nil || !v.Evidence.Complete {
			t.Fatalf("direct proposed API use lost completed attribution: %+v", v)
		}
		fix617(t, f)
		pass617(t, f)
	})
	for _, tc := range []struct {
		name, body string
		nonePasses bool
	}{
		// These two cases reproduce #620's review: merely defining future=None
		// makes the candidate pass, although it implements no callable API.
		{"after return", "    return; future()\n", true},
		{"if false", "    if False:\n        future()\n", true},
		{"after raise", "    raise RuntimeError('stop before API')\n    future()\n", false},
		{"inside branch", "    if True:\n        future()\n", false},
		{"after conditional return", "    if True:\n        return\n    future()\n", true},
		{"loop", "    for unused in []:\n        future()\n", true},
		{"while", "    while False:\n        future()\n", true},
		{"try", "    try:\n        future()\n    except TypeError:\n        pass\n", true},
		{"nested function", "    def later():\n        future()\n", true},
		{"lambda", "    later = lambda: future()\n", true},
		{"comprehension", "    unused = [future() for item in []]\n", true},
		{"generator", "    unused = (future() for item in [1])\n", true},
		{"short circuit", "    unused = False and future()\n", true},
		{"conditional expression", "    unused = future() if False else None\n", true},
		{"assert short circuit", "    assert True or future()\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "from product import future\ndef test_ready():\n" + tc.body
			f := fixture617(t, image, platform, []pyCase617{{"worker", "test_ready.py", "test_ready", source}}, nil, nil)
			c := check617(t, f, false)
			v := result617(t, c.Run, f.plan.Tests[0])
			if v.Status == "failed" && (v.Kind == "compile" || v.Kind == "test") {
				t.Fatalf("uncertain or unreachable use of future qualified the base: %+v", v)
			}
			if tc.nonePasses {
				write468(t, f.candidate, "worker/product.py", "VALUE = 0\nfuture = None\n")
				pass617(t, f)
			}
		})
	}
}
