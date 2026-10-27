package jsm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
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

const lifecycleFormID = "c18bde7a-d846-11ed-afa1-0242ac120002"

// This fixture tests the published JSON contract. [{}] is Atlassian's opaque
// layout example; the mock does not claim it renders the linked question.
const formLifecycleConfig = `
resource "terraform_data" "references" {
 input = { project = "SUPPORT", request_type = 63, field = "customfield_10000" }
}
resource "atlassian_jsm_form_template" "test" {
 project_id = terraform_data.references.output.project
 design = jsonencode({
  conditions = {}, layout = [{}], sections = {},
  questions = { "1" = { label = "Employee", type = "ts", jiraField = terraform_data.references.output.field, validation = { rq = true } } },
  settings = { name = "Example", submit = { lock = true, pdf = false } }
 })
 publish = jsonencode({ portal = {
  portalRequestTypeIds = [tonumber(terraform_data.references.output.request_type)],
  submitOnCreate = true, validateOnCreate = true
 } })
}
data "atlassian_jsm_form_template" "test" {
 project_id = atlassian_jsm_form_template.test.project_id
 id = atlassian_jsm_form_template.test.id
}
`

func TestFormTemplateLifecycle(t *testing.T) {
	var mu sync.Mutex
	var current map[string]any
	var creates, updates, deletes int
	var enrich bool
	var pending bool
	name := "tf-test-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	updatedName := name + "-updated"
	initialConfig := strings.Replace(formLifecycleConfig, `name = "Example"`, fmt.Sprintf("name = %q", name), 1)
	expectedName := name
	expectedIDs := []any{float64(63)}
	client := testutil.FormsClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		base := testutil.FormsTestPrefix + "/project/SUPPORT/form"
		switch {
		case (r.Method == "POST" && r.URL.Path == base) || (r.Method == "PUT" && r.URL.Path == base+"/"+lifecycleFormID):
			if r.Header.Get("Content-Type") != "application/json" {
				t.Error("missing content type")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if len(body) != 2 {
				t.Errorf("unexpected request keys: %v", body)
			}
			design, ok := body["design"].(map[string]any)
			if !ok {
				t.Error("missing design")
				return
			}
			if len(design) != 5 || design["settings"].(map[string]any)["name"] != expectedName {
				t.Errorf("unexpected design: %v", design)
			}
			question := design["questions"].(map[string]any)["1"].(map[string]any)
			if question["jiraField"] != "customfield_10000" || question["type"] != "ts" || question["validation"].(map[string]any)["rq"] != true {
				t.Errorf("incorrect linked question: %v", question)
			}
			portal := body["publish"].(map[string]any)["portal"].(map[string]any)
			if len(portal) != 3 || !reflect.DeepEqual(portal["portalRequestTypeIds"], expectedIDs) || portal["submitOnCreate"] != true || portal["validateOnCreate"] != true {
				t.Errorf("unexpected publishing: %v", portal)
			}
			if r.Method == "POST" {
				creates++
				pending = true
				if current != nil {
					t.Error("duplicate create")
				}
			} else {
				updates++
			}
			current = body
			current["id"] = lifecycleFormID
			current["updated"] = "2026-01-01T00:00:00Z"
			// Both write endpoints return index entries, never the complete design.
			fmt.Fprintf(w, `{"id":%q}`, lifecycleFormID)
		case r.Method == "GET" && r.URL.Path == base+"/"+lifecycleFormID:
			if current == nil || pending {
				pending = false
				w.WriteHeader(404)
				return
			}
			if enrich {
				current["design"].(map[string]any)["settings"].(map[string]any)["language"] = "en"
			}
			// Indented output exercises semantic JSON normalization.
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			_ = encoder.Encode(current)
		case r.Method == "DELETE" && r.URL.Path == base+"/"+lifecycleFormID:
			deletes++
			current = nil
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`"deleted"`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	})
	updated := strings.Replace(formLifecycleConfig, `name = "Example"`, fmt.Sprintf("name = %q", updatedName), 1)
	unpublished := strings.Replace(updated, `[tonumber(terraform_data.references.output.request_type)]`, `[]`, 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactoriesWithClient(client), CheckDestroy: func(_ *terraform.State) error {
		mu.Lock()
		defer mu.Unlock()
		if current != nil || creates != 1 || updates != 3 || deletes != 1 {
			return fmt.Errorf("lifecycle counts: create=%d update=%d delete=%d remains=%t", creates, updates, deletes, current != nil)
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: initialConfig, Check: resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("atlassian_jsm_form_template.test", "id", lifecycleFormID), resource.TestCheckResourceAttr("data.atlassian_jsm_form_template.test", "id", lifecycleFormID), resource.TestCheckResourceAttrPair("data.atlassian_jsm_form_template.test", "design", "atlassian_jsm_form_template.test", "design"))},
		{ResourceName: "atlassian_jsm_form_template.test", ImportState: true, ImportStateId: "SUPPORT/" + lifecycleFormID, ImportStateVerify: true},
		{Config: initialConfig, PlanOnly: true, PreConfig: func() { mu.Lock(); enrich = true; mu.Unlock() }},
		{Config: updated, PreConfig: func() { mu.Lock(); expectedName = updatedName; mu.Unlock() }},
		{Config: updated, PlanOnly: true},
		{Config: unpublished, PreConfig: func() { mu.Lock(); expectedIDs = []any{}; mu.Unlock() }},
		{Config: unpublished, PlanOnly: true},
		{Config: unpublished, PreConfig: func() {
			mu.Lock()
			current["design"].(map[string]any)["questions"].(map[string]any)["1"].(map[string]any)["label"] = "External change"
			mu.Unlock()
		}, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jsm_form_template.test", plancheck.ResourceActionUpdate)}}},
		{Config: strings.Replace(unpublished, `project_id = terraform_data.references.output.project`, `project_id = "OTHER"`, 1), PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jsm_form_template.test", plancheck.ResourceActionDestroyBeforeCreate)}}},
	}})
}
