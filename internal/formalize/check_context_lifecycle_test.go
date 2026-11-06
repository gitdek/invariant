package formalize

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gitdek/invariant/internal/project"
	"github.com/gitdek/invariant/internal/statechart"
)

func TestCheckContextServiceClosesActiveConnections(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprint(canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			capture, err := json.Marshal(CheckContext{Choices: map[string]string{"F0": strings.Repeat("source", 1024*1024)}})
			if err != nil {
				t.Fatal(err)
			}
			ref, closeService, err := serveCheckContext(ctx, string(capture))
			if err != nil {
				t.Fatal(err)
			}
			defer closeService()
			u, _ := url.Parse(ref.Endpoint)
			conn, err := net.DialTimeout("tcp", u.Host, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
			_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", u.Path, u.Host, ref.Token)
			if err != nil {
				t.Fatal(err)
			}
			// Stop consuming a response larger than the socket buffer. Cleanup
			// must close that connection before joining the serving goroutines.
			line, err := bufio.NewReader(conn).ReadString('\n')
			if err != nil || !strings.Contains(line, "200 OK") {
				t.Fatalf("service never began the response: %q, %v", line, err)
			}
			if canceled {
				cancel()
				deadline := time.Now().Add(time.Second)
				for {
					probe, err := net.DialTimeout("tcp", u.Host, 50*time.Millisecond)
					if err != nil {
						break
					}
					_ = probe.Close()
					if time.Now().After(deadline) {
						t.Fatal("cancellation did not close the listener")
					}
					time.Sleep(5 * time.Millisecond)
				}
			}
			done := make(chan struct{})
			go func() {
				closeService()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("service did not join cleanup with a blocked response")
			}
			if _, err := LoadCheckContext(t.Context(), ref, 0); err == nil {
				t.Fatal("closed service still supplies authority")
			}
		})
	}
}

func TestStagedCheckContextDetachesAllAuthority(t *testing.T) {
	c := CheckContext{ParentPlan: 667, Choices: map[string]string{"F0": "A"}, Revisions: []Revision{{Fork: "F0", Option: "B", By: "owner"}},
		Baseline: &statechart.ComparisonSnapshot{Module: "Model", Model: "exact source", Includes: map[string]string{"Helper.tla": "exact include"}, Lock: project.Lock{Bounds: map[string]string{"Limit": "2"}}}}
	ws := t.TempDir()
	capture, release, err := stageCheckContext(ws, c)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	c.Choices["F0"] = "invented"
	c.Revisions[0].Option = "invented"
	c.Baseline.Model = "invented"
	c.Baseline.Includes["Helper.tla"] = "invented"
	c.Baseline.Lock.Bounds["Limit"] = "99"
	saved, ok := checkContexts.Load(ws)
	if !ok {
		t.Fatal("direct check lost its caller context")
	}
	b, err := json.Marshal(saved)
	if err != nil || string(b) != capture {
		t.Fatalf("staged authority shares caller-owned memory: %v", err)
	}
	release()
	if _, ok := checkContexts.Load(ws); ok {
		t.Fatal("direct context outlived its run")
	}
	c.Baseline.Module = "../invalid"
	if _, _, err := stageCheckContext(ws, c); err == nil {
		t.Fatal("invalid baseline was staged")
	}
	if _, ok := checkContexts.Load(ws); ok {
		t.Fatal("setup failure retained direct check authority")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, closeService, err := serveCheckContext(ctx, capture); err == nil {
		closeService()
		t.Fatal("started an authority service for a canceled caller")
	}
}
