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

// Always-on local lifecycle test. No tenant credentials or TF_ACC required.
func TestFieldSchemeManagementLifecycle(t *testing.T) {
	var mu sync.Mutex
	var exists, associated bool
	var name, description string
	restrictions := []int64{}
	projectScheme := int64(90000)
	baseName := "tf-test-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("X-ExperimentalApi") != "opt-in" || r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" {
			t.Error("missing API headers")
		}
		user, token, ok := r.BasicAuth()
		if !ok || user != "test@example.org" || token != "test" {
			t.Error("missing test authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		base := "/rest/api/3/config/fieldschemes"
		switch {
		case (r.Method == "POST" && r.URL.Path == base || r.Method == "PUT" && r.URL.Path == base+"/10000") && r.URL.RawQuery == "":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if len(body) != 2 || (body["name"] != baseName && body["name"] != baseName+" updated") || (body["description"] != "Managed scheme" && body["description"] != "") {
				t.Errorf("wrong scheme body: %v", body)
				w.WriteHeader(400)
				return
			}
			if r.Method == "POST" && exists {
				t.Error("unexpected duplicate scheme creation")
			}
			exists = true
			name = body["name"]
			description = body["description"]
			// Intentionally partial numeric-ID response: must preserve planned metadata.
			_, _ = w.Write([]byte(`{"id":10000}`))
		case r.Method == "GET" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			if !exists {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "10000", "name": name, "description": description})
		case r.Method == "DELETE" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			if projectScheme == 10000 {
				t.Error("deleted a still-assigned scheme")
				w.WriteHeader(409)
				return
			}
			if associated {
				t.Error("scheme deleted before field association was removed")
			}
			exists = false
			_, _ = w.Write([]byte(`{"id":"10000","deleted":true}`))
		case r.Method == "GET" && r.URL.Path == base+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
			if !exists {
				w.WriteHeader(404)
				return
			}
			values := []any{}
			if associated {
				ids := []string{}
				for _, id := range restrictions {
					ids = append(ids, fmt.Sprint(id))
				}
				values = append(values, map[string]any{"fieldId": "customfield_10000", "restrictedToWorkTypes": ids})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "values": values})
		case (r.Method == "PUT" || r.Method == "DELETE") && r.URL.Path == base+"/fields" && r.URL.RawQuery == "":
			if !exists {
				t.Error("field association written before scheme creation")
				w.WriteHeader(400)
				return
			}
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if len(body) != 1 || body["customfield_10000"] == nil {
				t.Error("wrong field scope")
				w.WriteHeader(400)
				return
			}
			if r.Method == "DELETE" {
				var item map[string][]int64
				err := json.Unmarshal(body["customfield_10000"], &item)
				if err != nil || !reflect.DeepEqual(item, map[string][]int64{"schemeIds": {10000}}) {
					t.Errorf("wrong removal body: %s", body["customfield_10000"])
					w.WriteHeader(400)
					return
				}
				associated = false
				w.WriteHeader(204)
				return
			}
			var items []map[string][]int64
			if err := json.Unmarshal(body["customfield_10000"], &items); err != nil || len(items) != 1 {
				t.Error("wrong field association body")
				w.WriteHeader(400)
				return
			}
			item := items[0]
			expected := map[string][]int64{"schemeIds": {10000}}
			if len(item["restrictedToWorkTypes"]) > 0 {
				expected["restrictedToWorkTypes"] = []int64{10001}
			}
			if !reflect.DeepEqual(item, expected) {
				t.Errorf("wrong field association body: %v", item)
				w.WriteHeader(400)
				return
			}
			associated = true
			restrictions = item["restrictedToWorkTypes"]
			if restrictions == nil {
				restrictions = []int64{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"fieldId": "customfield_10000", "schemeId": 10000, "success": true, "workTypeIds": restrictions}}})
		case r.Method == "GET" && r.URL.Path == base+"/projects" && r.URL.RawQuery == "projectId=10002&startAt=0&maxResults=100":
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "values": []any{map[string]any{"projectId": 10002, "schemeId": projectScheme}}})
		case r.Method == "PUT" && r.URL.Path == base+"/projects" && r.URL.RawQuery == "":
			var body map[string]map[string][]int64
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 {
				t.Error("wrong project association body")
				w.WriteHeader(400)
				return
			}
			for scheme, item := range body {
				if (scheme != "10000" && scheme != "90000") || len(item) != 1 || !reflect.DeepEqual(item["projectIds"], []int64{10002}) {
					t.Errorf("wrong project association: %v", body)
					w.WriteHeader(400)
					return
				}
				projectScheme = 10000
				if scheme == "90000" {
					projectScheme = 90000
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"projectId": 10002, "schemeId": projectScheme, "success": true}}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	config := func(updated bool) string {
		suffix, desc, ids := "", "Managed scheme", `["10001"]`
		if updated {
			suffix, desc, ids = " updated", "", `[]`
		}
		return fmt.Sprintf(`
resource "atlassian_jira_field_scheme" "test" {
 name = %q
 description = %q
}
resource "atlassian_jira_field_scheme_field" "test" {
 scheme_id = atlassian_jira_field_scheme.test.id
 field_id = "customfield_10000"
 issue_type_ids = %s
}
resource "atlassian_jira_project_field_scheme" "test" {
 project_id = "10002"
 scheme_id = atlassian_jira_field_scheme.test.id
 on_destroy_scheme_id = "90000"
}
data "atlassian_jira_field_scheme" "test" {
 id = atlassian_jira_field_scheme.test.id
 depends_on = [atlassian_jira_field_scheme.test]
}
data "atlassian_jira_field_scheme_field" "test" {
 scheme_id = atlassian_jira_field_scheme_field.test.scheme_id
 field_id = atlassian_jira_field_scheme_field.test.field_id
 depends_on = [atlassian_jira_field_scheme_field.test]
}
`, baseName+suffix, desc, ids)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if exists || associated || projectScheme != 90000 {
				return fmt.Errorf("incomplete cleanup: scheme=%v association=%v projectScheme=%d", exists, associated, projectScheme)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme.test", "name", baseName),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme.test", "description", "Managed scheme"),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme_field.test", "issue_type_ids.#", "1"),
				resource.TestCheckResourceAttr("data.atlassian_jira_field_scheme.test", "name", baseName),
				resource.TestCheckResourceAttr("data.atlassian_jira_field_scheme_field.test", "issue_type_ids.#", "1"),
				resource.TestCheckResourceAttr("atlassian_jira_project_field_scheme.test", "scheme_id", "10000"),
			)},
			{ResourceName: "atlassian_jira_field_scheme.test", ImportState: true, ImportStateVerify: true},
			{ResourceName: "atlassian_jira_field_scheme_field.test", ImportState: true, ImportStateVerify: true},
			{ResourceName: "atlassian_jira_project_field_scheme.test", ImportState: true, ImportStateId: "10002", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "project_id", ImportStateVerifyIgnore: []string{"on_destroy_scheme_id"}},
			{Config: config(true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme.test", "name", baseName+" updated"),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme.test", "description", ""),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme_field.test", "issue_type_ids.#", "0"),
			)},
			{Config: config(true), PlanOnly: true},
			{PreConfig: func() {
				mu.Lock()
				name = "Changed outside Terraform"
				restrictions = []int64{10001}
				projectScheme = 90000
				mu.Unlock()
			}, Config: config(true), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: config(true), Check: resource.TestCheckResourceAttr("atlassian_jira_project_field_scheme.test", "scheme_id", "10000")},
		},
	})
}
