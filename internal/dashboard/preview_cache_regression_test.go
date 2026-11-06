package dashboard

import (
	"os"
	"strings"
	"testing"
)

func TestPreviewCacheDamageStaysWithItsOption(t *testing.T) {
	tc, _ := reader603Tools(t)
	ws := t.TempDir()
	preview639Write(t, ws, preview639Draft(t, 1, nil))
	p := preview639Checked(t, ws, tc)
	for _, damage := range []string{"missing", "corrupt"} {
		t.Run(damage, func(t *testing.T) {
			s := preview639Server(t, p, "owner/project", 639)
			options := preview639Choices(t, preview639Snapshot(t, s))
			key := strings.TrimPrefix(options[0].Reader.Fingerprint, "sha256:")
			var err error
			if damage == "missing" {
				err = os.Remove(s.previewFile(key))
			} else {
				err = os.WriteFile(s.previewFile(key), []byte("{"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			cold := &Server{Cache: s.Cache, Repos: []*Repo{{Name: "owner/project"}}}
			cold.loadSnapshot()
			res := reader631GET(cold, "GET", "/api/state.json", "")
			if res.Code != 200 {
				t.Fatalf("lost comparison: %d", res.Code)
			}
			got := preview639Choices(t, res.Body.Bytes())
			if got[0].Reader.Status != "cached_evidence" || got[0].Reader.SVG != "" || got[0].Reader.Page == "" {
				t.Fatalf("damaged cache has no isolated diagnostic: %+v", got[0].Reader)
			}
			if *got[1].Reader != *options[1].Reader {
				t.Fatal("healthy sibling's identity changed")
			}
			preview639Image(t, cold, got[1])
		})
	}
}
