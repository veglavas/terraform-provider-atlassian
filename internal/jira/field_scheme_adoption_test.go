package jira_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// Adopt pre-existing remote objects without POSTing a duplicate scheme or
// rewriting an association. Declaring local destroy policy must not issue a PUT.
func TestFieldSchemeManagementDeclarativeAdoption(t *testing.T) {
	var mu sync.Mutex
	exists, associated, assigned := true, true, true
	cleanupAllowed := false
	name := "tf-test-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-ExperimentalApi") != "opt-in" {
			t.Error("missing experimental header")
		}
		if r.Method != "GET" && !cleanupAllowed {
			t.Errorf("import unexpectedly mutated remote objects: %s %s", r.Method, r.URL)
			w.WriteHeader(400)
			return
		}
		base := "/rest/api/3/config/fieldschemes"
		switch {
		case r.Method == "GET" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			if !exists {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "10000", "name": name, "description": ""})
		case r.Method == "GET" && r.URL.Path == base+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
			values := []any{}
			if associated {
				values = append(values, map[string]any{"fieldId": "customfield_10000"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "values": values})
		case r.Method == "GET" && r.URL.Path == base+"/projects" && r.URL.RawQuery == "projectId=10002&startAt=0&maxResults=100":
			scheme := 10000
			if !assigned {
				scheme = 90000
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "values": []any{map[string]any{"projectId": 10002, "schemeId": scheme}}})
		case r.Method == "DELETE" && r.URL.Path == base+"/fields" && r.URL.RawQuery == "":
			var body map[string]map[string][]int64
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]map[string][]int64{"customfield_10000": {"schemeIds": {10000}}}) {
				t.Error("unexpected association removal body")
				w.WriteHeader(400)
				return
			}
			associated = false
			w.WriteHeader(204)
		case r.Method == "PUT" && r.URL.Path == base+"/projects" && r.URL.RawQuery == "":
			var body map[string]map[string][]int64
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]map[string][]int64{"90000": {"projectIds": {10002}}}) {
				t.Error("unexpected fallback assignment body")
				w.WriteHeader(400)
				return
			}
			assigned = false
			w.WriteHeader(204)
		case r.Method == "DELETE" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			if assigned || associated {
				t.Error("unsafe scheme deletion ordering")
				w.WriteHeader(409)
				return
			}
			exists = false
			_, _ = w.Write([]byte(`{"id":"10000","deleted":true}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	config := fmt.Sprintf(`
resource "atlassian_jira_field_scheme" "test" { name = %q }
resource "atlassian_jira_field_scheme_field" "test" {
 scheme_id = atlassian_jira_field_scheme.test.id
 field_id = "customfield_10000"
}
resource "atlassian_jira_project_field_scheme" "test" {
 project_id = "10002"
 scheme_id = atlassian_jira_field_scheme.test.id
 on_destroy_scheme_id = "90000"
}
import {
 to = atlassian_jira_field_scheme.test
 id = "10000"
}
import {
 to = atlassian_jira_field_scheme_field.test
 id = "10000:customfield_10000"
}
import {
 to = atlassian_jira_project_field_scheme.test
 id = "10002"
}
`, name)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if exists || associated || assigned {
				return fmt.Errorf("adopted fixture was not cleaned up")
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: config, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("atlassian_jira_field_scheme.test", "name", name),
			resource.TestCheckResourceAttr("atlassian_jira_field_scheme_field.test", "issue_type_ids.#", "0"),
			resource.TestCheckResourceAttr("atlassian_jira_project_field_scheme.test", "on_destroy_scheme_id", "90000"),
			func(_ *terraform.State) error { mu.Lock(); defer mu.Unlock(); cleanupAllowed = true; return nil },
		)}},
	})
}
