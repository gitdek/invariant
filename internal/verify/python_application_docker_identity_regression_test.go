package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonApplicationDockerIdentityDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		malformed     bool
	}{
		{"warning on stderr", `[]`, false},
		{"null option", `[null]`, true},
		{"null after rootless", `["name=rootless",null]`, true},
		{"trailing JSON", `[] ["name=rootless"]`, true},
		{"invalid UTF-8", "[\"name=\xff\"]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, output := t.TempDir(), t.TempDir()
			script := `#!/bin/sh
case "$1" in
  info)
    if [ "$#" != 3 ] || [ "$2" != --format ] || [ "$3" != '{{json .SecurityOptions}}' ]; then
      exit 89
    fi
    printf '%s\n' "$PA399_OPTIONS"
    printf '%s\n' 'daemon warning' >&2
    ;;
  run)
    printf '%s\n' 'application events' > "$PA399_OUTPUT/events.jsonl"
    ;;
  *) exit 89 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("PA399_OPTIONS", tc.options)
			t.Setenv("PA399_OUTPUT", output)
			p, err := RunPythonApplicationCommand(context.Background(), PythonApplicationCommand{
				Root: t.TempDir(), RuntimeDir: t.TempDir(), OutputDir: output,
				Image: "selected-image", Interpreter: "/usr/local/bin/python", WorkingDir: "/src",
			})
			if tc.malformed {
				if err == nil || !strings.Contains(err.Error(), "malformed") || p.ExitCode != -1 {
					t.Fatalf("malformed identity was not refused: %+v, %v", p, err)
				}
				if _, err := os.Stat(filepath.Join(output, "events.jsonl")); !os.IsNotExist(err) {
					t.Fatalf("application ran after malformed identity discovery: %v", err)
				}
			} else if err != nil || p.ExitCode != 0 || string(p.Events) != "application events\n" {
				t.Fatalf("daemon stderr corrupted structured identity discovery: %+v, %v", p, err)
			}
		})
	}
}
