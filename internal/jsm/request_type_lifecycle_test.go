package jsm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

func TestRequestTypeLifecycle(t *testing.T) {
	var mu sync.Mutex
	var current map[string]any
	var pending bool
	name := "tf-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		base := "/rest/servicedeskapi/servicedesk/2/requesttype"
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query: %s", r.URL)
		}
		switch {
		case r.Method == "POST" && r.URL.Path == base:
			if r.Header.Get("X-ExperimentalApi") != "opt-in" {
				t.Error("missing opt-in")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 4 || body["name"] != name || body["description"] != "Description" || body["helpText"] != "Help" || body["issueTypeId"] != "10001" {
				t.Errorf("unexpected body: %v", body)
			}
			if current != nil {
				t.Error("unexpected duplicate creation")
			}
			current = map[string]any{"id": "10", "serviceDeskId": "2", "issueTypeId": body["issueTypeId"], "name": body["name"], "description": body["description"], "helpText": body["helpText"], "portalId": "3", "groupIds": []string{}}
			pending = true
			_, _ = w.Write([]byte(`{"id":"10"}`))
		case r.Method == "GET" && r.URL.Path == base+"/10":
			if current == nil || pending {
				pending = false
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(current)
		case r.Method == "DELETE" && r.URL.Path == base+"/10":
			if r.Header.Get("X-ExperimentalApi") != "opt-in" {
				t.Error("missing opt-in")
			}
			current = nil
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	config := fmt.Sprintf(`
resource "atlassian_jsm_request_type" "test" {
 service_desk_id = "2"
 issue_type_id = "10001"
 name = %q
 description = "Description"
 help_text = "Help"
}
data "atlassian_jsm_request_type" "test" {
 service_desk_id = atlassian_jsm_request_type.test.service_desk_id
 id = atlassian_jsm_request_type.test.id
}
`, name)
	steps := []resource.TestStep{
		{Config: config, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("atlassian_jsm_request_type.test", "id", "10"),
			resource.TestCheckResourceAttr("atlassian_jsm_request_type.test", "portal_id", "3"),
			resource.TestCheckResourceAttr("atlassian_jsm_request_type.test", "group_ids.#", "0"),
			resource.TestCheckResourceAttr("data.atlassian_jsm_request_type.test", "name", name),
			resource.TestCheckResourceAttr("data.atlassian_jsm_request_type.test", "description", "Description"),
			resource.TestCheckResourceAttr("data.atlassian_jsm_request_type.test", "help_text", "Help"),
			resource.TestCheckResourceAttr("data.atlassian_jsm_request_type.test", "issue_type_id", "10001"),
		)},
		{ResourceName: "atlassian_jsm_request_type.test", ImportState: true, ImportStateId: "2/10", ImportStateVerify: true},
		{Config: config, PlanOnly: true},
	}
	for _, replacement := range [][2]string{{`name = ` + fmt.Sprintf("%q", name), `name = "Renamed"`}, {`description = "Description"`, `description = "Changed"`}, {`help_text = "Help"`, `help_text = "Changed"`}, {`issue_type_id = "10001"`, `issue_type_id = "10002"`}, {`service_desk_id = "2"`, `service_desk_id = "4"`}} {
		steps = append(steps, resource.TestStep{Config: strings.Replace(config, replacement[0], replacement[1], 1), PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jsm_request_type.test", plancheck.ResourceActionDestroyBeforeCreate)}}})
	}
	steps = append(steps, resource.TestStep{Config: config, PlanOnly: true, PreConfig: func() { mu.Lock(); defer mu.Unlock(); current["groupIds"] = []string{"8"} }})
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories, CheckDestroy: func(_ *terraform.State) error {
		mu.Lock()
		defer mu.Unlock()
		if current != nil {
			return fmt.Errorf("request type remains")
		}
		return nil
	}, Steps: steps})
}

func TestRequestTypeValidation(t *testing.T) {
	t.Setenv("ATLASSIAN_URL", "http://127.0.0.1:1")
	t.Setenv("ATLASSIAN_USER", "test")
	t.Setenv("ATLASSIAN_TOKEN", "test")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories, Steps: []resource.TestStep{{Config: `resource "atlassian_jsm_request_type" "test" {
 service_desk_id = "SUPPORT"
 issue_type_id = "10001"
 name = "Test"
}`, ExpectError: regexp.MustCompile("must be a numeric ID")}}})
}
