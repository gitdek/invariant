package conformance

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gitdek/invariant/internal/tlc"
)

func TestStreamingJSONErrorCompatibility(t *testing.T) {
	for i, raw := range []string{
		"", "{", `{"traces":[`, `{"traces":[[`, `{"traces":[[{"n":`,
		`{"traces":[[{"n":1}]],}`, `{"traces":[[{"n":1}] 2]}`, `{"unknown" 1}`,
		`true`, `1`, `"x"`, `[]`, `{"init":true}`, `{"attempts":[true]}`,
		`{"INIT":[false]}`, `{"TRACES":[[true]]}`, `{"STATES":[true]}`,
		`{"unknown":1.}`, `{"unknown":1e+}`, `{"unknown":-x}`, `{"unknown":01}`,
		`{"unknown":trUe}`, `{"unknown":"\q"}`, `{"unknown":"\u00xx"}`,
		"{\"unknown\":\"a\nb\"}", `{"unknown":[1,]}`, `{"unknown":{1:2}}`,
		`{"unknown":` + strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000) + "}",
	} {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			var legacy struct {
				Traces [][]map[string]any `json:"traces"`
				attempts
			}
			want := decodeValue([]byte(raw), &legacy)
			if want == nil {
				t.Fatal("fixture must be malformed")
			}
			runner := declaredAttemptRunner(func(context.Context, string, string, tlc.Config) (tlc.Result, error) {
				return tlc.Result{}, fmt.Errorf("malformed recording reached TLC")
			})
			got, err := CheckReader(context.Background(), runner, t.TempDir(), "Counter", nil, []string{"n"}, 1, s559Short{strings.NewReader(raw)}, nil)
			message := "the driver's output isn't valid JSON: " + want.Error()
			if got.Message != message || err != nil {
				t.Fatalf("got %q, %v; want %q", got.Message, err, message)
			}
		})
	}
}
