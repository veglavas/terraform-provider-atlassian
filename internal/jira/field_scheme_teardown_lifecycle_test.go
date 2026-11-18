package jira_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// This models the reported tenant behavior, not an assertion that Jira always
// rejects override deletion. It exercises Terraform's real dependency graph,
// including a managed scheme. ExpectError deliberately records the current
// limitation; this is a reproduction, not evidence of a production fix.
func TestFieldSchemeTeardownDependencyLifecycle(t *testing.T) {
	var mu sync.Mutex
	var schemeExists, rejectOverrideDelete bool
	associations := map[string]bool{}
	overrides := map[string]map[int64]bool{}
	var rejectedDeletes, associationDeletes int
	name := "tf-test-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	base := "/rest/api/3/config/fieldschemes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, token, ok := r.BasicAuth()
		if !ok || user != "test@example.org" || token != "test" || r.Header.Get("X-ExperimentalApi") != "opt-in" || r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" || r.Header.Get("Accept") != "application/json" {
			t.Error("missing authentication or standard headers")
		}
		w.Header().Set("Content-Type", "application/json")
		fail := func(message string) { t.Error(message); w.WriteHeader(400) }
		switch {
		case r.Method == "POST" && r.URL.Path == base && r.URL.RawQuery == "":
			var body map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || !reflect.DeepEqual(body, map[string]string{"name": name, "description": "Teardown fixture"}) {
				fail("invalid scheme body")
				return
			}
			schemeExists = true
			_, _ = w.Write([]byte(`{"id":10000}`))
		case r.Method == "GET" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			if !schemeExists {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "10000", "name": name, "description": "Teardown fixture"})
		case r.Method == "DELETE" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			for _, associated := range associations {
				if associated {
					fail("scheme deleted before association")
					return
				}
			}
			schemeExists = false
			_, _ = w.Write([]byte(`{"id":"10000","deleted":true}`))
		case r.Method == "GET" && r.URL.Path == base+"/10000/fields":
			field := r.URL.Query().Get("fieldId")
			if (field != "customfield_10000" && field != "customfield_10001") || !reflect.DeepEqual(r.URL.Query(), url.Values{"fieldId": {field}, "startAt": {"0"}, "maxResults": {"100"}}) {
				fail("invalid field query")
				return
			}
			values := []any{}
			if associations[field] {
				workTypes := []any{}
				ids := make([]int, 0, len(overrides[field]))
				for id := range overrides[field] {
					ids = append(ids, int(id))
				}
				sort.Ints(ids)
				for _, id := range ids {
					workTypes = append(workTypes, map[string]any{"workTypeId": fmt.Sprint(id), "isRequired": overrides[field][int64(id)]})
				}
				values = append(values, map[string]any{"fieldId": field, "parameters": map[string]any{"isRequired": false}, "restrictedToWorkTypes": []string{}, "workTypeParameters": workTypes})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "values": values})
		case (r.Method == "PUT" || r.Method == "DELETE") && (r.URL.Path == base+"/fields" || r.URL.Path == base+"/fields/parameters") && r.URL.RawQuery == "":
			if !schemeExists || r.Header.Get("Content-Type") != "application/json" {
				fail("write without scheme or JSON header")
				return
			}
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				fail("invalid bulk body")
				return
			}
			var field string
			for key := range body {
				field = key
			}
			if field != "customfield_10000" && field != "customfield_10001" {
				fail("invalid field ID")
				return
			}
			if r.URL.Path == base+"/fields" {
				var actual any
				_ = json.Unmarshal(body[field], &actual)
				expectedJSON := `[{"schemeIds":[10000]}]`
				if r.Method == "DELETE" {
					expectedJSON = `{"schemeIds":[10000]}`
				}
				var expected any
				_ = json.Unmarshal([]byte(expectedJSON), &expected)
				if !reflect.DeepEqual(actual, expected) {
					fail("invalid association body")
					return
				}
				if r.Method == "DELETE" {
					associations[field] = false
					delete(overrides, field)
					associationDeletes++
					w.WriteHeader(204)
					return
				}
				associations[field] = true
				if overrides[field] == nil {
					overrides[field] = map[int64]bool{}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"schemeId": 10000, "fieldId": field, "workTypeIds": []int64{}, "success": true}}})
				return
			}
			if !associations[field] {
				fail("parameter write without association")
				return
			}
			var items []map[string]json.RawMessage
			if json.Unmarshal(body[field], &items) != nil || len(items) != 1 {
				fail("invalid parameter body")
				return
			}
			if r.Method == "DELETE" {
				var idList []int64
				if json.Unmarshal(items[0]["workTypeIds"], &idList) != nil || len(idList) != 1 {
					fail("invalid work type removal")
					return
				}
				id := idList[0]
				var actual, expected any
				_ = json.Unmarshal(body[field], &actual)
				_ = json.Unmarshal([]byte(fmt.Sprintf(`[{"schemeId":10000,"workTypeIds":[%d],"parameters":["isRequired"]}]`, id)), &expected)
				if (id != 10002 && id != 10003) || !reflect.DeepEqual(actual, expected) {
					fail("invalid removal body")
					return
				}
				if rejectOverrideDelete {
					rejectedDeletes++
					w.WriteHeader(400)
					return
				}
				delete(overrides[field], id)
				w.WriteHeader(204)
				return
			}
			var params []struct {
				WorkTypeID int64 `json:"workTypeId"`
				Required   bool  `json:"isRequired"`
			}
			if json.Unmarshal(items[0]["workTypeParameters"], &params) != nil || len(params) != 1 {
				fail("invalid override body")
				return
			}
			id := params[0].WorkTypeID
			var actual, expected any
			_ = json.Unmarshal(body[field], &actual)
			_ = json.Unmarshal([]byte(fmt.Sprintf(`[{"schemeIds":[10000],"workTypeParameters":[{"workTypeId":%d,"isRequired":true}]}]`, id)), &expected)
			if (id != 10002 && id != 10003) || !reflect.DeepEqual(actual, expected) {
				fail("invalid override scope")
				return
			}
			overrides[field][id] = params[0].Required
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []any{map[string]any{"schemeId": 10000, "fieldId": field, "workTypeId": id, "success": true}}})
		default:
			fail(fmt.Sprintf("unexpected request %s %s", r.Method, r.URL))
		}
	}))
	defer server.Close()
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	config := func(field string, both bool) string {
		count := 2
		if !both {
			count = 1
		}
		return fmt.Sprintf(`
resource "terraform_data" "field" { input = %q }
resource "atlassian_jira_field_scheme" "test" {
 name = %q
 description = "Teardown fixture"
}
resource "atlassian_jira_field_scheme_field" "test" {
 scheme_id = atlassian_jira_field_scheme.test.id
 field_id = terraform_data.field.output
}
resource "atlassian_jira_field_scheme_requirement" "test" {
 count = %d
 scheme_id = atlassian_jira_field_scheme_field.test.scheme_id
 field_id = atlassian_jira_field_scheme_field.test.field_id
 issue_type_id = ["10003", "10002"][count.index]
 is_required = true
}
`, field, name, count)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if schemeExists || len(overrides) != 0 {
				return fmt.Errorf("scheme or overrides remain")
			}
			for _, associated := range associations {
				if associated {
					return fmt.Errorf("association remains")
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("customfield_10000", true)},
			{Config: config("customfield_10000", false), Check: func(_ *terraform.State) error {
				mu.Lock()
				defer mu.Unlock()
				if !associations["customfield_10000"] || !reflect.DeepEqual(overrides["customfield_10000"], map[int64]bool{10003: true}) || associationDeletes != 0 {
					return fmt.Errorf("individual requirement removal changed association or sibling")
				}
				return nil
			}},
			{Config: config("customfield_10000", true)},
			{PreConfig: func() { mu.Lock(); rejectOverrideDelete = true; mu.Unlock() }, Config: config("customfield_10001", true), ExpectError: regexp.MustCompile(`field scheme parameter DELETE returned HTTP 400`)},
			{PreConfig: func() {
				mu.Lock()
				defer mu.Unlock()
				if rejectedDeletes == 0 || associationDeletes != 0 || !associations["customfield_10000"] || len(overrides["customfield_10000"]) != 2 {
					t.Fatal("did not reproduce blocked teardown before association deletion")
				}
				// Model the user's manual UI detachment between failed and successful applies.
				associations["customfield_10000"] = false
				delete(overrides, "customfield_10000")
			}, Config: config("customfield_10001", true), Check: func(_ *terraform.State) error {
				mu.Lock()
				defer mu.Unlock()
				if !associations["customfield_10001"] || len(overrides["customfield_10001"]) != 2 {
					return fmt.Errorf("replacement did not restore association and overrides")
				}
				return nil
			}},
			{Config: `provider "atlassian" {}`, ExpectError: regexp.MustCompile(`field scheme parameter DELETE returned HTTP 400`)},
			{PreConfig: func() {
				mu.Lock()
				defer mu.Unlock()
				if !schemeExists || !associations["customfield_10001"] || associationDeletes != 0 {
					t.Fatal("destroy did not stop before association and scheme deletion")
				}
				associations["customfield_10001"] = false
				delete(overrides, "customfield_10001")
			}, Config: `provider "atlassian" {}`},
		},
	})
}
