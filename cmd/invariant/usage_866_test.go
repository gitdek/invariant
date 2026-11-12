package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// invariant help names every command the CLI takes (#866): AGENTS.md and
// the README send readers to mcp, which it left out. The commands are read
// from main's own switch, so a new one can't be added without its line.
func TestUsageNamesEveryCommand866(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "switch cmd, args := os.Args[1], os.Args[2:]; cmd {")
	end := strings.Index(string(src[start:]), "\n\tdefault:")
	if start < 0 || end < 0 {
		t.Fatal("main's command switch moved")
	}
	cases := regexp.MustCompile(`(?m)^\tcase "([a-z-]+)"`).FindAllStringSubmatch(string(src[start:start+end]), -1)
	if len(cases) < 10 {
		t.Fatalf("read only %d commands from main's switch", len(cases))
	}
	for _, c := range cases {
		if name := c[1]; name != "help" && !strings.Contains(usage, "\n  invariant "+name+" ") {
			t.Errorf("invariant help doesn't name %q", name)
		}
	}
	// Each entry starts in the same column, with no blank line among them.
	body := usage[strings.Index(usage, "Usage:\n")+len("Usage:\n"):]
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		if !strings.HasPrefix(line, "  invariant ") && !strings.HasPrefix(line, "                                               ") {
			t.Errorf("usage line is out of line with the rest: %q", line)
		}
	}
	for _, flag := range []string{"-tlc-parallel", "[-issue N]", "[-C DIR] [-base REF] [-issue N] [HEAD]"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("invariant help omits %s", flag)
		}
	}
}

// pin -h and trace -h ask for help. They took -h as a path, said they
// couldn't open it and exited 2.
func TestPinAndTraceTakeHelp866(t *testing.T) {
	for _, help := range []string{"-h", "-help", "--help"} {
		if code := pinCmd([]string{help}); code != 0 {
			t.Errorf("pin %s exits %d", help, code)
		}
		if code := traceCmd([]string{help}); code != 0 {
			t.Errorf("trace %s exits %d", help, code)
		}
	}
	if code := pinCmd(nil); code != 2 {
		t.Errorf("pin with no project exits %d, want 2", code)
	}
	if code := traceCmd([]string{"a", "b"}); code != 2 {
		t.Errorf("trace with two files exits %d, want 2", code)
	}
}
