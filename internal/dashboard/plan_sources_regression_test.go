package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanSourcesPublicationWaitsForPersistence(t *testing.T) {
	p := source715Proposal(false, "persist before publication")
	s := source715Server(t, p)
	dir := filepath.Join(s.Cache, "plan-sources")
	if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	source715Publish(t, s)
	state, _ := source715ReadState(t, s)
	v := source715WaitingFor(t, state, 715).Plan
	if v.SourceHash != p.Hash || v.SourceURL != "" {
		t.Fatalf("published a URL before retaining its payload: %+v", v)
	}
	if w := source715GET(s, "GET", source715Path(p.Hash)); w.Code != 404 {
		t.Fatalf("failed persistence served HTTP %d", w.Code)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	source715Publish(t, s)
	source715Exact(t, s, p)
	source715Exact(t, source715Cold(t, s.Cache), p)
}

func TestPlanSourcesDetachPublishedPayload(t *testing.T) {
	p := source715Proposal(true, "detached source bytes")
	s := source715Server(t)
	if !s.keepPlanSources(planSourceRecord{Repo: s.Repos[0].Name, Factory: "factory[bot]", Proposal: p}) {
		t.Fatal("could not keep complete proposal")
	}
	url := source715Path(p.Hash)
	want := source715GET(s, "GET", url).Body.String()
	p.Plan.Sources[p.Plan.Tests[0].File] = "changed caller map"
	p.Plan.Tests[0].Framework = "pytest"
	p.Plan.Summary = "changed caller summary"
	for _, server := range []*Server{s, source715Cold(t, s.Cache)} {
		if got := source715GET(server, "GET", url); got.Code != 200 || got.Body.String() != want {
			t.Fatal("a caller changed an already published response")
		}
	}
}

func TestPlanSourcesRestartChecksMetadataAndProvenance(t *testing.T) {
	for _, damage := range []string{"summary", "factory", "repository", "filename", "extra source"} {
		t.Run(damage, func(t *testing.T) {
			p := source715Proposal(true, "cache metadata")
			s := source715Server(t, p)
			source715Publish(t, s)
			file := filepath.Join(s.Cache, "plan-sources", strings.TrimPrefix(p.Hash, "sha256:")+".json")
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var record planSourceRecord
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "summary":
				record.Proposal.Plan.Summary += " changed"
			case "factory":
				record.Factory = "other[bot]"
			case "repository":
				record.Repo = "other/repository"
			case "extra source":
				record.Proposal.Plan.Sources["unlisted.go"] = "package extra"
			case "filename":
				if err := os.Rename(file, filepath.Join(filepath.Dir(file), strings.Repeat("0", 64)+".json")); err != nil {
					t.Fatal(err)
				}
			}
			if damage != "filename" {
				if err := os.WriteFile(file, source715JSON(t, record), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cold := source715Cold(t, s.Cache)
			state, body := source715ReadState(t, cold)
			source715NoBodies(t, body, p)
			if source715WaitingFor(t, state, 715).Plan.SourceHash != p.Hash {
				t.Fatal("damaged sources discarded lightweight state")
			}
			if w := source715GET(cold, "GET", source715Path(p.Hash)); w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("damaged authority served or cached: %d %v", w.Code, w.Header())
			}
		})
	}
}
