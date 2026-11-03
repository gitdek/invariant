package toolchain

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPinnedDownloadPermanentFailures(t *testing.T) {
	for _, kind := range []string{"certificate", "401", "rename", "missing host"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file")
			url := "https://cdn.example.invalid/pinned"
			if kind == "missing host" {
				url = "https:///pinned"
			}
			if kind == "rename" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			download433Client(t, func(*http.Request) (*http.Response, error) {
				calls++
				if kind == "certificate" {
					return nil, &tls.CertificateVerificationError{Err: errors.New("untrusted certificate")}
				}
				code := http.StatusOK
				if kind == "401" {
					code = http.StatusUnauthorized
				}
				return download433Response(code, http.StatusText(code), io.NopCloser(strings.NewReader("wanted"))), nil
			})
			err := fetch(context.Background(), url, path, download433Hash("wanted"))
			want := 1
			if kind == "missing host" {
				want = 0
			}
			if err == nil || calls != want {
				t.Fatalf("permanent failure: requests=%d, want %d; error=%v", calls, want, err)
			}
			download433NoTemps(t, dir)
		})
	}
}

func TestPinnedRunAcquisitionCancellation(t *testing.T) {
	dir := docker433Setup(t, "cancel")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := Docker(ctx, "run", "--rm", "--platform", "linux/amd64", JavaImage, "verify")
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	docker433Wait(t, func() bool { return len(docker433OfKind(dir, "pull")) == 1 })
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt acquisition")
	}
	if len(docker433OfKind(dir, "run")) != 0 || len(docker433OfKind(dir, "pull")) != 1 {
		t.Fatalf("cancelled acquisition continued: %#v", docker433ReadCalls(dir))
	}
}

func TestPinnedRunAcquiresRequestedPlatform(t *testing.T) {
	for _, method := range []string{"Run", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "recover")
			// The cached image is linux/amd64, not the requested linux/arm64.
			docker433Ready(t, dir)
			cmd := Docker(context.Background(), "run", "--rm", "--platform", "linux/arm64", JavaImage, "verify")
			cmd.Stdin = strings.NewReader("payload")
			var out bytes.Buffer
			var err error
			if method == "CombinedOutput" {
				var b []byte
				b, err = cmd.CombinedOutput()
				out.Write(b)
			} else {
				cmd.Stdout = &out
				if method == "Run" {
					err = cmd.Run()
				} else {
					err = cmd.Start()
					if err == nil {
						err = cmd.Wait()
					}
				}
			}
			if err != nil || out.String() != "verified:payload" {
				t.Fatalf("run: %q, %v", out.String(), err)
			}
			pulls := docker433OfKind(dir, "pull")
			if len(pulls) != 3 || len(docker433OfKind(dir, "run")) != 1 {
				t.Fatalf("wrong acquisition/workload attempts: %#v", docker433ReadCalls(dir))
			}
			for _, pull := range pulls {
				if docker433Option(pull.Args, "--platform") != "linux/arm64" {
					t.Fatalf("lost requested platform: %q", pull.Args)
				}
			}
		})
	}
}

func TestDockerImageOperandParsing(t *testing.T) {
	for _, args := range [][]string{
		{"-it", "-e", "PIN=" + JavaImage, "-v/tmp:/work", "--platform=linux/amd64", "--", JavaImage, "--pull=never"},
		{"--env=PIN=" + JavaImage, "--platform", "linux/amd64", JavaImage, "--platform=linux/arm64"},
	} {
		image, platform, pull := dockerImage(args)
		if image != JavaImage || platform != "linux/amd64" || pull != "" {
			t.Fatalf("misread run operands %q: %q, %q, %q", args, image, platform, pull)
		}
	}
}

func TestPinnedPullReportsIntegrityFailureThroughEveryCompletion(t *testing.T) {
	for _, method := range []string{"Run", "Output", "CombinedOutput", "Start/Wait"} {
		t.Run(method, func(t *testing.T) {
			dir := docker433Setup(t, "integrity-immediate")
			const diagnostic = "digest sha256:bad did not match expected sha256:good"
			t.Setenv("INVARIANT_433_INTEGRITY", diagnostic)
			cmd := Docker(context.Background(), "image", "pull", JavaImage)
			var err error
			switch method {
			case "Run":
				err = cmd.Run()
			case "Output":
				_, err = cmd.Output()
			case "CombinedOutput":
				_, err = cmd.CombinedOutput()
			case "Start/Wait":
				err = cmd.Start()
				if err == nil {
					err = cmd.Wait()
				}
			}
			if err == nil || !strings.Contains(err.Error(), diagnostic) || len(docker433OfKind(dir, "pull")) != 1 {
				t.Fatalf("integrity diagnostic lost or retried: %v; %#v", err, docker433ReadCalls(dir))
			}
		})
	}
}
