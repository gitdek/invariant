package dashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveSelectsLatestCompletionsAcrossWatchers269(t *testing.T) {
	const repo = "owner/app"
	// Each watcher uses the same issue and opaque run name. Its work root
	// remains part of the completion's identity and transcript provenance.
	writeTranscript := func(t *testing.T, f *finished269Fixture, step finished269Step, path string, reads, output int) {
		t.Helper()
		finished269Write(t, filepath.Join(f.workspace, path), []byte("package app\n"))
		events := []map[string]any{
			{"type": "invariant.workspace", "cwd": f.workspace},
			{"type": "turn.started", "timestamp": step.Since},
		}
		for i := 0; i < reads; i++ {
			events = append(events, map[string]any{
				"type": "item.completed", "timestamp": step.Since.Add(time.Duration(i+1) * time.Minute),
				"item": map[string]any{"id": fmt.Sprintf("read-%d", i), "type": "command_execution", "command": "cat " + path},
			})
		}
		events = append(events, map[string]any{
			"type": "turn.completed", "timestamp": step.Since.Add(5 * time.Minute),
			"usage": map[string]any{"output_tokens": output},
		})
		var lines []byte
		for _, event := range events {
			b, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, b...)
			lines = append(lines, '\n')
		}
		finished269Write(t, filepath.Join(f.work, filepath.FromSlash(repo), "issues", fmt.Sprintf("issue-%d", step.Issue), step.Run, "transcript.jsonl"), lines)
	}
	for _, stoppedA := range []bool{false, true} {
		for _, stoppedB := range []bool{false, true} {
			for _, tied := range []bool{false, true} {
				for _, reverse := range []bool{false, true} {
					t.Run(fmt.Sprintf("stopped_a_%v/stopped_b_%v/tied_%v/reverse_%v", stoppedA, stoppedB, tied, reverse), func(t *testing.T) {
						a, b := newFinished269(t, repo), newFinished269(t, repo)
						endA := finished269At(10, 0)
						if tied {
							endA = finished269At(11, 0)
						}
						stepA := finished269End(finished269Run(269, "building", "same-opaque-run", finished269At(8, 0)), endA)
						stepB := finished269End(finished269Run(269, "building", "same-opaque-run", finished269At(9, 0)), finished269At(11, 0))
						writeTranscript(t, a, stepA, "watcher-a.go", 1, 111)
						writeTranscript(t, b, stepB, "watcher-b.go", 2, 222)
						pidA, pidB := os.Getpid(), os.Getpid()
						if stoppedA {
							pidA = 0
						}
						if stoppedB {
							pidB = 0
						}
						a.status(t, repo, pidA, nil, []finished269Step{stepA})
						b.status(t, repo, pidB, nil, []finished269Step{stepB})
						works := []string{a.work, b.work}
						if reverse {
							works[0], works[1] = works[1], works[0]
						}
						want := []struct {
							step          finished269Step
							path          string
							reads, output int
						}{
							{stepA, "watcher-a.go", 1, 111},
							{stepB, "watcher-b.go", 2, 222},
						}
						if !tied {
							want = want[1:]
						}
						// No dashboard has observed either step running. Recreate
						// it too: completion selection cannot depend on its cache.
						for restart := 0; restart < 2; restart++ {
							s := &Server{Works: works, Repos: a.s.Repos}
							cards, _ := finished269Read(t, s, finished269At(18+restart, 0), false)
							if len(cards) != len(want) {
								t.Fatalf("latest completions across all watcher statuses = %+v; want %d cards at %v", cards, len(want), *stepB.Ended)
							}
							// The established display order is the original start
							// time, independent of watcher-directory ordering.
							for i, expected := range want {
								card := finished269Want(t, cards[i:i+1], expected.step)
								if card.Repo != repo || card.Turns != expected.reads || card.Output != expected.output || card.Tools["read"] != expected.reads {
									t.Errorf("completion used another watcher's counts: %+v; want repo %s, %d turns/reads and %d output", card, repo, expected.reads, expected.output)
								}
								if len(card.Files) != 1 || card.Files[0].Path != expected.path || card.Files[0].Reads != expected.reads {
									t.Errorf("completion used another watcher's paths: %+v; want %s read %d times", card.Files, expected.path, expected.reads)
								}
							}
						}
					})
				}
			}
		}
	}
}
