package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"text/template"
)

// pythonApplicationContainers is the intended test-only shared listing used by
// the two existing Python cancellation leak checks. The builder supplies it in
// a regular test file or one of those existing files, and makes both checks use
// it. These acceptance tests provide only the Docker double, not the listing.
func init() {
	if os.Getenv("INVARIANT_433_PS_HELPER") == "1" {
		os.Exit(python433PS(os.Args[1:]))
	}
}

func python433PS(args []string) int {
	if len(args) == 0 || args[0] != "ps" {
		fmt.Fprintln(os.Stderr, "expected docker ps")
		return 1
	}
	if os.Getenv("INVARIANT_433_PS_FAILURE") == "1" {
		fmt.Fprintln(os.Stderr, "daemon unavailable")
		return 1
	}
	prefix := "invariant-" + os.Getenv("INVARIANT_433_PARENT_PID") + "-"
	rows := []map[string]string{
		{"ID": "own-old", "Names": prefix + "100-1"},
		{"ID": "foreign-old", "Names": "invariant-999999999-100-1"},
	}
	phase, _ := os.ReadFile(os.Getenv("INVARIANT_433_PS_PHASE"))
	if string(phase) != "before" {
		rows = append(rows,
			map[string]string{"ID": "foreign-new", "Names": "invariant-999999999-200-1"},
			map[string]string{"ID": "foreign-prefix", "Names": "invariant-" + os.Getenv("INVARIANT_433_PARENT_PID") + "0-200-1"},
			map[string]string{"ID": "foreign-embedded", "Names": "other-" + prefix + "200-1"},
		)
	}
	if string(phase) == "own-new" {
		rows = append(rows, map[string]string{"ID": "own-new", "Names": prefix + "200-2"})
	}
	var names []*regexp.Regexp
	format := "{{.ID}}"
	for i := 1; i < len(args); i++ {
		arg, value := args[i], ""
		if arg == "--filter" || arg == "-f" || arg == "--format" {
			if i+1 >= len(args) {
				return 1
			}
			i++
			value = args[i]
		} else if strings.HasPrefix(arg, "--filter=") {
			value = strings.TrimPrefix(arg, "--filter=")
			arg = "--filter"
		} else if strings.HasPrefix(arg, "--format=") {
			value = strings.TrimPrefix(arg, "--format=")
			arg = "--format"
		}
		if arg == "--format" {
			format = value
		}
		if (arg == "--filter" || arg == "-f") && strings.HasPrefix(value, "name=") {
			re, err := regexp.Compile(strings.TrimPrefix(value, "name="))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			names = append(names, re)
		}
	}
	if format == "json" {
		format = "{{json .}}"
	}
	tmpl, err := template.New("ps").Funcs(template.FuncMap{"json": func(v any) string { b, _ := json.Marshal(v); return string(b) }}).Parse(format)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, row := range rows {
		match := len(names) == 0
		for _, re := range names {
			if re.MatchString("/" + row["Names"]) {
				match = true
			}
		}
		if !match {
			continue
		}
		if err := tmpl.Execute(os.Stdout, row); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Fprintln(os.Stdout)
	}
	return 0
}

func TestPythonApplicationLeakChecksIgnoreOtherProcesses(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexec "+quoted+" \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	phase := filepath.Join(dir, "phase")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INVARIANT_433_PS_HELPER", "1")
	t.Setenv("INVARIANT_433_PARENT_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("INVARIANT_433_PS_PHASE", phase)
	for _, step := range []string{"before", "foreign-new", "own-new"} {
		if err := os.WriteFile(phase, []byte(step), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := pythonApplicationContainers(context.Background())
		if err != nil {
			t.Fatalf("%s: list containers: %v", step, err)
		}
		want := map[string]bool{"own-old": true}
		if step == "own-new" {
			want["own-new"] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: containers=%v, want %v; unrelated processes must not create false leaks", step, got, want)
		}
	}
	t.Setenv("INVARIANT_433_PS_FAILURE", "1")
	if _, err := pythonApplicationContainers(context.Background()); err == nil {
		t.Error("failed listing was mistaken for no leaked containers")
	}
}
