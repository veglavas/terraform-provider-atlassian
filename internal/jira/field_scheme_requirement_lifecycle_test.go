package jira_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// Local mocks only: resource.UnitTest deliberately runs without TF_ACC.
func TestFieldSchemeRequirementLifecycle(t *testing.T) {
	var mu sync.Mutex
	var override *bool
	schemeName := "tf-test-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	var deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		user, token, ok := r.BasicAuth()
		if !ok || user != "test@example.org" || token != "test" || r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" || r.Header.Get("X-ExperimentalApi") != "opt-in" {
			t.Error("missing standard authentication/experimental headers")
		}
		w.Header().Set("Content-Type", "application/json")
		base := "/rest/api/3/config/fieldschemes"
		switch {
		case r.Method == "GET" && r.URL.Path == base+"/projects" && r.URL.RawQuery == "projectId=10001&startAt=0&maxResults=100":
			_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"total":1,"values":[{"projectId":10001,"schemeId":10000}]}`))
		case r.Method == "GET" && r.URL.Path == base+"/10000" && r.URL.RawQuery == "":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "10000", "name": schemeName, "description": "Shared scheme"})
		case r.Method == "GET" && r.URL.Path == base+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
			types := []any{map[string]any{"workTypeId": "10003", "isRequired": false, "description": "Other issue type"}}
			// Description/renderer overrides remain even after removing isRequired.
			target := map[string]any{"workTypeId": "10002", "description": "Preserve description", "rendererType": "jira-text-renderer"}
			if override != nil {
				target["isRequired"] = *override
			}
			types = append(types, target)
			_ = json.NewEncoder(w).Encode(map[string]any{"startAt": 0, "isLast": true, "total": 1, "values": []any{map[string]any{
				"fieldId": "customfield_10000", "parameters": map[string]any{"isRequired": true, "description": "Default description", "rendererType": "jira-text-renderer"},
				"workTypeParameters": types,
			}}})
		case (r.Method == "PUT" || r.Method == "DELETE") && r.URL.Path == base+"/fields/parameters" && r.URL.RawQuery == "":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing JSON Content-Type")
			}
			var body map[string][]map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if r.Method == "PUT" {
				entries := body["customfield_10000"]
				if len(entries) != 1 {
					t.Errorf("wrong write body: %v", body)
					w.WriteHeader(400)
					return
				}
				params, ok := entries[0]["workTypeParameters"].([]any)
				if !ok || len(params) != 1 {
					t.Errorf("wrong parameters: %v", body)
					w.WriteHeader(400)
					return
				}
				target, ok := params[0].(map[string]any)
				if !ok {
					t.Error("invalid parameter object")
					w.WriteHeader(400)
					return
				}
				required, ok := target["isRequired"].(bool)
				if !ok {
					t.Error("missing isRequired")
					w.WriteHeader(400)
					return
				}
				expected := map[string][]map[string]any{"customfield_10000": {{"schemeIds": []any{float64(10000)}, "workTypeParameters": []any{map[string]any{"workTypeId": float64(10002), "isRequired": required}}}}}
				if !reflect.DeepEqual(body, expected) {
					t.Errorf("write changed unrelated settings: %v", body)
					w.WriteHeader(400)
					return
				}
				override = &required
				_, _ = w.Write([]byte(`{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeId":10002,"success":true}]}`))
			} else {
				expected := map[string][]map[string]any{"customfield_10000": {{"schemeId": float64(10000), "workTypeIds": []any{float64(10002)}, "parameters": []any{"isRequired"}}}}
				if !reflect.DeepEqual(body, expected) {
					t.Errorf("delete changed unrelated settings: %v", body)
					w.WriteHeader(400)
					return
				}
				override = nil
				deletes++
				w.WriteHeader(204)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	config := func(required bool) string {
		return fmt.Sprintf(`
resource "terraform_data" "references" { input = { project_id = "10001" } }
data "atlassian_jira_project_field_scheme" "test" {
 project_id = terraform_data.references.output.project_id
}
resource "atlassian_jira_field_scheme_requirement" "test" {
 scheme_id = data.atlassian_jira_project_field_scheme.test.id
 field_id = "customfield_10000"
 issue_type_id = "10002"
 is_required = %t
}
data "atlassian_jira_field_scheme_requirement" "test" {
 scheme_id = atlassian_jira_field_scheme_requirement.test.scheme_id
 field_id = atlassian_jira_field_scheme_requirement.test.field_id
 issue_type_id = atlassian_jira_field_scheme_requirement.test.issue_type_id
 depends_on = [atlassian_jira_field_scheme_requirement.test]
}
`, required)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if override != nil || deletes == 0 {
				return fmt.Errorf("required override was not removed")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.atlassian_jira_project_field_scheme.test", "id", "10000"),
				resource.TestCheckResourceAttr("data.atlassian_jira_project_field_scheme.test", "name", schemeName),
				resource.TestCheckResourceAttr("data.atlassian_jira_project_field_scheme.test", "description", "Shared scheme"),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme_requirement.test", "id", "10000:customfield_10000:10002"),
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme_requirement.test", "is_required", "true"),
				resource.TestCheckResourceAttr("data.atlassian_jira_field_scheme_requirement.test", "is_required", "true"),
			)},
			{ResourceName: "atlassian_jira_field_scheme_requirement.test", ImportState: true, ImportStateVerify: true},
			{Config: config(false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("atlassian_jira_field_scheme_requirement.test", "is_required", "false"),
				resource.TestCheckResourceAttr("data.atlassian_jira_field_scheme_requirement.test", "is_required", "false"),
			)},
			{Config: config(false), PlanOnly: true},
			{PreConfig: func() { mu.Lock(); override = nil; mu.Unlock() }, Config: config(false), PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: config(false), Check: resource.TestCheckResourceAttr("atlassian_jira_field_scheme_requirement.test", "is_required", "false")},
			{Config: strings.Replace(config(false), `issue_type_id = "10002"`, `issue_type_id = "10004"`, 1), PlanOnly: true, ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jira_field_scheme_requirement.test", plancheck.ResourceActionDestroyBeforeCreate)}}},
		},
	})
}
