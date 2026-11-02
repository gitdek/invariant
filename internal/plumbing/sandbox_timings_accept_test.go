package plumbing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The timing hint's persistence contract is deliberately separate from result
// records: $GOCACHE/invariant-test-times-v1.json contains a JSON array of
// {"package": GoImportPath, "name": TopLevelName, "elapsed_seconds": Number}.
// Only finite, nonnegative elapsed times for discovered top-level tests are
// usable. Unusable data is ignored. The file is an ordering hint, never the
// source of the test inventory, executable commands, or pass/fail evidence.
const s397TimingFile = "invariant-test-times-v1.json"

type s397Timing struct {
	Package string  `json:"package"`
	Name    string  `json:"name"`
	Elapsed float64 `json:"elapsed_seconds"`
}

// Go's cache entries live in 256 hexadecimal buckets. Share only those
// buckets with the warmed host cache, so each fixture owns its timing hint.
// Copying the whole cache is expensive; giving every fixture a cold standard
// library would make the short deadline checks measure compilation instead.
func s397IsolatedCache(t *testing.T, existing string) string {
	t.Helper()
	cache := t.TempDir()
	for n := 0; n < 256; n++ {
		bucket := fmt.Sprintf("%02x", n)
		source := filepath.Join(existing, bucket)
		info, err := os.Stat(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("Go cache bucket %s is not a directory", source)
		}
		if err := os.Symlink(source, filepath.Join(cache, bucket)); err != nil {
			t.Fatal(err)
		}
	}
	return cache
}

func s397SeedTimings(t *testing.T, f *s397Fixture, records []s397Timing) {
	t.Helper()
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	s397Write(t, filepath.Join(f.cache, s397TimingFile), string(data), 0o600)
}

// Seven new, unknown tests occupy seven workers. All remaining test bodies
// then execute through the eighth worker, making their relative order visible
// without assuming that eight simultaneous OS process starts are ordered.
// These blockers sort before every observed package and are never recorded in
// the seeded cache. The last observed test releases them.
func s397TimingBlockers(t *testing.T, f *s397Fixture) {
	t.Helper()
	var src strings.Builder
	fmt.Fprintf(&src, `package ahead
import ("os"; "path/filepath"; "testing"; "time")
func hold(t *testing.T, name string) {
  dir := %q
  if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil { t.Fatal(err) }
  until := time.Now().Add(15*time.Second)
  for {
    if _, err := os.Stat(filepath.Join(dir, "timing-release")); err == nil { return }
    if time.Now().After(until) { t.Fatal("ordered queue did not reach the release test") }
    time.Sleep(5*time.Millisecond)
  }
}
`, f.observe)
	for n := 0; n < 7; n++ {
		fmt.Fprintf(&src, "func TestHold%02d(t *testing.T) { hold(t, %q) }\n", n, fmt.Sprintf("timing-holder-%02d", n))
	}
	f.pkg(t, "ahead", src.String())
}

// Measured bodies sleep only during the learning run. During the ordered run
// they wait until all seven blocker bodies have started, append a single
// observation, and optionally release the blockers. Thus the observations
// represent execution, not a scheduler's self-reported intended ordering.
func s397TimingSource(f *s397Fixture, pkg string) string {
	return fmt.Sprintf(`package %s
import ("fmt"; "os"; "path/filepath"; "testing"; "time")
func sample(t *testing.T, elapsed time.Duration, release bool) {
  if os.Getenv("S397_TIMING_MODE") == "learn" { time.Sleep(elapsed); return }
  dir := %q
  until := time.Now().Add(10*time.Second)
  for {
    ready := true
    for n := 0; n < 7; n++ {
      if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("timing-holder-%%02d", n))); err != nil { ready = false }
    }
    if ready { break }
    if time.Now().After(until) { t.Fatal("unknown blocker tests were not queued first") }
    time.Sleep(5*time.Millisecond)
  }
  out, err := os.OpenFile(filepath.Join(dir, "timing-order"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { t.Fatal(err) }
  _, err = out.WriteString(%q + "/" + t.Name() + "\n"); out.Close()
  if err != nil { t.Fatal(err) }
  if release {
    if err := os.WriteFile(filepath.Join(dir, "timing-release"), nil, 0600); err != nil { t.Fatal(err) }
  }
}
`, pkg, f.observe, pkg)
}

func s397TimingOrder(t *testing.T, f *s397Fixture, r Result, want []string) {
	t.Helper()
	if !r.Passed() {
		t.Fatalf("ordered fixture failed: %s\n%s", r.Summary(), r.Output)
	}
	data, err := os.ReadFile(filepath.Join(f.observe, "timing-order"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(data)); !reflect.DeepEqual(got, want) {
		t.Errorf("observed execution order = %v, want %v", got, want)
	}
	for _, entry := range want {
		pkg, name, _ := strings.Cut(entry, "/")
		s397Status(t, r, pkg, name, "test", "passed")
	}
	for n := 0; n < 7; n++ {
		s397Status(t, r, "ahead", fmt.Sprintf("TestHold%02d", n), "test", "passed")
	}
}

func TestSandboxReusesMeasuredTimesInSharedCache(t *testing.T) {
	f := s397New(t)
	f.pkg(t, "measured", s397TimingSource(f, "measured")+`
func TestAFast(t *testing.T) { sample(t, 10*time.Millisecond, true) }
func TestBMedium(t *testing.T) { sample(t, 300*time.Millisecond, false) }
func TestZSlowest(t *testing.T) { sample(t, 900*time.Millisecond, false) }
`)
	f.contract(t, false)
	f.warm(t)
	t.Setenv("S397_TIMING_MODE", "learn")
	first := f.run(t, 35*time.Second, 20*time.Second)
	if !first.Passed() {
		t.Fatalf("learning run failed: %s\n%s", first.Summary(), first.Output)
	}
	data, err := os.ReadFile(filepath.Join(f.cache, s397TimingFile))
	if err != nil {
		t.Fatal("the first run did not persist elapsed times in its supplied build cache: ", err)
	}
	var saved []s397Timing
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal("the first run wrote invalid timing records: ", err)
	}
	times := map[string]float64{}
	for _, record := range saved {
		if record.Package == "example.com/shards397/measured" {
			if _, duplicate := times[record.Name]; duplicate {
				t.Errorf("the first run wrote duplicate timing records for %s", record.Name)
			}
			times[record.Name] = record.Elapsed
		}
	}
	fast, hasFast := times["TestAFast"]
	medium, hasMedium := times["TestBMedium"]
	slow, hasSlow := times["TestZSlowest"]
	if !hasFast || !hasMedium || !hasSlow || !(0 <= fast && fast < medium && medium < slow) {
		t.Fatalf("the first run must record the measured top-level durations: %v", times)
	}

	// New tests remain unknown even though the other tests share a warm cache.
	// They hold seven workers; the measured slowest must get the remaining
	// worker before either shorter measured test, despite sorting last by name.
	s397TimingBlockers(t, f)
	f.warm(t, "./ahead")
	t.Setenv("S397_TIMING_MODE", "ordered")
	second := f.run(t, 35*time.Second, 20*time.Second)
	s397TimingOrder(t, f, second, []string{
		"measured/TestZSlowest", "measured/TestBMedium", "measured/TestAFast",
	})
}

func TestSandboxQueuesUnknownBeforeLongestKnownTests(t *testing.T) {
	f := s397New(t)
	s397TimingBlockers(t, f)
	f.pkg(t, "alpha", s397TimingSource(f, "alpha")+`
func TestZUnknown(t *testing.T) { sample(t, 0, false) }
func TestAKnownFast(t *testing.T) { sample(t, 0, true) }
func TestAUnknown(t *testing.T) { sample(t, 0, false) }
`)
	f.pkg(t, "beta", s397TimingSource(f, "beta")+`
func TestAKnownMiddle(t *testing.T) { sample(t, 0, false) }
func TestAUnknown(t *testing.T) { sample(t, 0, false) }
func TestZKnownSlow(t *testing.T) { sample(t, 0, false) }
`)
	f.contract(t, false)
	f.warm(t)
	// Neither file order nor package/name order matches elapsed-time order.
	// Contract's known zero keeps its acceptance fixture out of the unknowns.
	s397SeedTimings(t, f, []s397Timing{
		{"example.com/shards397/alpha", "TestAKnownFast", 1},
		{"example.com/shards397/beta", "TestAKnownMiddle", 20},
		{"example.com/shards397/contract", "TestRatified", 0},
		{"example.com/shards397/beta", "TestZKnownSlow", 90},
	})
	t.Setenv("S397_TIMING_MODE", "ordered")
	r := f.run(t, 35*time.Second, 20*time.Second)
	s397TimingOrder(t, f, r, []string{
		"alpha/TestAUnknown", "alpha/TestZUnknown", "beta/TestAUnknown",
		"beta/TestZKnownSlow", "beta/TestAKnownMiddle", "alpha/TestAKnownFast",
	})
}

func TestSandboxTimingHintsCannotChangeInventoryOrVerdicts(t *testing.T) {
	f := s397New(t)
	visit := func(pkg string) string {
		return fmt.Sprintf(`package %s
import ("os"; "testing")
func visit(t *testing.T, mayFail bool) {
  out, err := os.OpenFile(%q, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
  if err != nil { t.Fatal(err) }
  _, err = out.WriteString(%q + "/" + t.Name() + "\n"); out.Close()
  if err != nil { t.Fatal(err) }
  if mayFail && os.Getenv("S397_TIMING_FAIL") == "1" { t.Fatal("real test-body failure") }
}
`, pkg, filepath.Join(f.observe, "timing-visits"), pkg)
	}
	f.pkg(t, "alpha", visit("alpha")+`
func TestSameName(t *testing.T) { visit(t, true) }
func TestMissingFromCache(t *testing.T) { visit(t, false) }
`)
	f.pkg(t, "beta", visit("beta")+`
func TestSameName(t *testing.T) { visit(t, false) }
`)
	f.pkg(t, "contract", "")
	f.accept(t, "contract", "TestPassing", visit("contract")+`
func TestPassing(t *testing.T) { visit(t, false) }
func TestConditionalFailure(t *testing.T) { visit(t, true) }
`)
	f.plan.Tests = append(f.plan.Tests, Test{Name: "TestConditionalFailure", File: "contract/pinned_accept_test.go", Says: "The fixture's conditional acceptance check passes."})
	f.warm(t)

	marker := filepath.Join(f.observe, "injected-command")
	quotedMarker, err := json.Marshal("TestSameName; touch " + marker + "; #")
	if err != nil {
		t.Fatal(err)
	}
	attacks := []struct{ name, data string }{
		{"truncated", `[{"package":"example.com/shards397/alpha","name":"TestSameName","elapsed_seconds":`},
		{"wrong_shape", `{"passed":true,"tests":[],"accepted":{"contract/pinned_accept_test.go:TestConditionalFailure":true}}`},
		{"invalid_values", `[
  null, [], "ignore me",
  {"package":17,"name":false,"elapsed_seconds":1},
  {"package":"example.com/shards397/alpha","name":"TestSameName","elapsed_seconds":-1},
  {"package":"example.com/shards397/alpha","name":"TestMissingFromCache","elapsed_seconds":1e999},
  {"package":"example.com/shards397/beta","name":"TestSameName","elapsed_seconds":"NaN"},
  {"package":"example.com/shards397/contract","name":"TestConditionalFailure","elapsed_seconds":null}
]`},
		{"hostile_inventory_and_results", fmt.Sprintf(`[
  {"package":"example.com/shards397/alpha","name":"TestSameName","elapsed_seconds":100,"status":"passed","passed":true,"skip":true},
  {"package":"example.com/shards397/alpha","name":"TestSameName","elapsed_seconds":200},
  {"package":"example.com/shards397/beta","name":"TestSameName","elapsed_seconds":1,"status":"failed"},
  {"package":"example.com/shards397/contract","name":"TestPassing","elapsed_seconds":9,"accepted":false},
  {"package":"example.com/shards397/contract","name":"TestConditionalFailure","elapsed_seconds":8,"accepted":true,"status":"passed"},
  {"package":"example.com/shards397/alpha","name":"TestInjected","elapsed_seconds":999},
  {"package":"../../outside","name":"TestSameName","elapsed_seconds":999},
  {"package":"example.com/shards397/alpha","name":%s,"elapsed_seconds":999},
  {"package":"example.com/shards397/alpha","name":"TestSameName/injected-child","elapsed_seconds":999}
]`, quotedMarker)},
	}
	for _, attack := range attacks {
		t.Run(attack.name, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("failing_%t", fail), func(t *testing.T) {
					s397Write(t, filepath.Join(f.cache, s397TimingFile), attack.data, 0o600)
					s397Write(t, filepath.Join(f.observe, "timing-visits"), "", 0o600)
					t.Setenv("S397_TIMING_FAIL", "0")
					if fail {
						t.Setenv("S397_TIMING_FAIL", "1")
					}
					r := f.run(t, 35*time.Second, 20*time.Second)
					if r.DeadlineExceeded || !r.Vet || r.Tests == fail || r.Passed() == fail {
						t.Errorf("timing hint changed the actual verdict: %s\n%s", r.Summary(), r.Output)
					}
					want := map[string]int{
						"alpha/TestSameName": 1, "alpha/TestMissingFromCache": 1,
						"beta/TestSameName": 1, "contract/TestPassing": 2,
						"contract/TestConditionalFailure": 2,
					}
					data, err := os.ReadFile(filepath.Join(f.observe, "timing-visits"))
					if err != nil {
						t.Fatal(err)
					}
					seen := map[string]int{}
					for _, entry := range strings.Fields(string(data)) {
						seen[entry]++
					}
					if !reflect.DeepEqual(seen, want) {
						t.Errorf("hint removed, duplicated or injected test execution: got %v, want %v", seen, want)
					}
					results := 0
					for _, got := range r.TestResults {
						pkg := strings.TrimPrefix(got.Package, "example.com/shards397/")
						if _, known := want[pkg+"/"+got.Name]; !known || (got.Phase != "test" && (got.Phase != "accept" || pkg != "contract")) {
							t.Errorf("hint injected result evidence: %+v", got)
						}
						results++
					}
					if results != 7 {
						t.Errorf("got %d final test records, want five ordinary and two acceptance records", results)
					}
					if len(r.Accepted) != 2 {
						t.Errorf("hint changed the acceptance inventory: %+v", r.Accepted)
					}
					for entry, runs := range want {
						pkg, name, _ := strings.Cut(entry, "/")
						status := "passed"
						if fail && (entry == "alpha/TestSameName" || entry == "contract/TestConditionalFailure") {
							status = "failed"
						}
						s397Status(t, r, pkg, name, "test", status)
						if runs == 2 {
							s397Status(t, r, pkg, name, "accept", status)
							key := "contract/pinned_accept_test.go:" + name
							accepted, present := r.Accepted[key]
							if !present || accepted != (status == "passed") {
								t.Errorf("hint changed acceptance evidence %s: accepted=%v, present=%v", key, accepted, present)
							}
						}
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Errorf("a timing record executed a command: marker error=%v", err)
					}
				})
			}
		})
	}
}
