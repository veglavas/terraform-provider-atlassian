package atlassian

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPostAsync(t *testing.T) {
	for _, scenario := range []string{"sync", "task-body", "task-location", "conflict", "missing-task", "foreign-location"} {
		t.Run(scenario, func(t *testing.T) {
			var follows atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mutation" {
					follows.Add(1)
					return
				}
				if r.Method != "POST" {
					t.Errorf("method = %s", r.Method)
				}
				switch scenario {
				case "sync":
					w.WriteHeader(200)
				case "task-body":
					w.WriteHeader(303)
					fmt.Fprint(w, `{"id":"123"}`)
				case "task-location":
					w.Header().Set("Location", "/rest/api/3/task/123")
					w.WriteHeader(303)
				case "conflict":
					w.WriteHeader(409)
				case "missing-task":
					w.WriteHeader(303)
				case "foreign-location":
					w.Header().Set("Location", "https://example.invalid/rest/api/3/task/123")
					w.WriteHeader(303)
				}
			}))
			defer server.Close()
			client, err := NewClient(ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			id, err := client.PostAsync(context.Background(), "/mutation", map[string]string{"key": "value"})
			wantError := scenario == "conflict" || scenario == "missing-task" || scenario == "foreign-location"
			if (err != nil) != wantError {
				t.Fatalf("id=%q error=%v", id, err)
			}
			if (scenario == "task-body" || scenario == "task-location") && id != "123" {
				t.Errorf("id=%q", id)
			}
			if follows.Load() != 0 {
				t.Fatal("followed redirect")
			}
		})
	}
}
