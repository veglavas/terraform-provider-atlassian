package atlassian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestExperimentalRequestOptionOnRetry(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/experimental" || r.URL.RawQuery != "" {
			t.Errorf("unexpected path %s", r.URL)
		}
		if r.Header.Get("X-ExperimentalApi") != "opt-in" {
			t.Error("experimental header missing")
		}
		switch r.Method {
		case http.MethodPost:
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 1 || body["name"] != "Test" {
				t.Errorf("body: %v", body)
			}
			if posts.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(429)
				return
			}
			_, _ = w.Write([]byte(`{"id":"10"}`))
		case http.MethodDelete:
			w.WriteHeader(404)
		default:
			t.Errorf("method: %s", r.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(ClientConfig{URL: server.URL, User: "test", Token: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := client.Post(context.Background(), "/experimental", map[string]string{"name": "Test"}, &result, WithExperimentalAPI()); err != nil {
		t.Fatal(err)
	}
	if posts.Load() != 2 || result["id"] != "10" {
		t.Fatal("retry failed")
	}
	status, err := client.DeleteWithStatus(context.Background(), "/experimental", WithExperimentalAPI())
	if err != nil || status != 404 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}
