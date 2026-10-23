package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestWorkflowPayloadStatusSearch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		want     string
		code     int
		missing  bool
	}{
		{name: "todo", category: `"TODO"`, want: "TODO"},
		{name: "in-progress", category: `"IN_PROGRESS"`, want: "IN_PROGRESS"},
		{name: "done", category: `"DONE"`, want: "DONE"},
		{name: "object-category", category: `{"key":"indeterminate"}`, want: "IN_PROGRESS"},
		{name: "invalid-category", category: `"UNKNOWN"`},
		{name: "missing", missing: true},
		{name: "not-found", code: http.StatusNotFound},
		{name: "forbidden", code: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls.Add(1)
				if req.Method != http.MethodGet || req.URL.Path != "/rest/api/3/statuses/search" || req.URL.Query().Get("id") != "10300" || req.URL.Query().Get("maxResults") != "50" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if tc.code != 0 {
					w.WriteHeader(tc.code)
					return
				}
				switch req.URL.Query().Get("startAt") {
				case "0":
					// Ignore unrelated results and continue to the requested ID.
					w.Write([]byte(`{"isLast":false,"values":[{"id":"999","name":"Other","statusCategory":"DONE"}]}`))
				case "1":
					values := []statusAPIItem{}
					if !tc.missing {
						values = append(values, statusAPIItem{ID: "10300", Name: "Verification/Validation", Description: "Unused status", RawStatusCategory: json.RawMessage(tc.category)})
					}
					json.NewEncoder(w).Encode(map[string]any{"isLast": true, "values": values})
				default:
					t.Errorf("unexpected pagination: %s", req.URL)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			r := &workflowResource{client: client}
			payload, err := r.workflowPayload(context.Background(), "test", "", []string{"10300"}, nil)
			if tc.want == "" {
				if err == nil {
					t.Fatal("expected status lookup error")
				}
				if (tc.missing || tc.code == http.StatusNotFound) && !strings.Contains(err.Error(), `status "10300" not found`) {
					t.Fatalf("unexpected missing status error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			statuses := payload["statuses"].([]map[string]any)
			if len(statuses) != 1 || statuses[0]["id"] != "10300" || statuses[0]["statusCategory"] != tc.want || statuses[0]["description"] != "Unused status" {
				t.Fatalf("unexpected status definitions: %+v", statuses)
			}
			if calls.Load() != 2 {
				t.Fatalf("status search requests = %d, want 2", calls.Load())
			}
		})
	}
}
