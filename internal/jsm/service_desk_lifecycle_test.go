package jsm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// Exercise the real provider graph: unknown project ID -> deferred desk lookup
// -> request type creation, then an empty second plan and dependency-safe destroy.
func TestServiceDeskFromManagedProject(t *testing.T) {
	var mu sync.Mutex
	var project, request, issueType, scheme map[string]any
	assigned := false
	deskAttempts := 0
	key := "TF" + strings.ToUpper(acctest.RandStringFromCharSet(8, acctest.CharSetAlpha))
	name := "tf-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		decode := func() map[string]string {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			return body
		}
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/api/3/issuetypescheme") {
			q := r.URL.Query()
			if q.Get("startAt") != "0" || q.Get("maxResults") != "50" {
				t.Errorf("scheme pagination: %s", r.URL)
			}
			switch r.URL.Path {
			case "/rest/api/3/issuetypescheme/project":
				if q.Get("projectId") != "10500" || len(q) != 3 {
					t.Errorf("project mapping query: %s", r.URL)
				}
			case "/rest/api/3/issuetypescheme/mapping":
				if q.Get("issueTypeSchemeId") != "10002" || len(q) != 3 {
					t.Errorf("issue mapping query: %s", r.URL)
				}
			default:
				if len(q) != 2 {
					t.Errorf("scheme query: %s", r.URL)
				}
			}
		} else if r.URL.Path != "/rest/servicedeskapi/servicedesk" && r.URL.RawQuery != "" {
			t.Errorf("unexpected query: %s", r.URL)
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issuetype":
			body := decode()
			if len(body) != 3 || body["name"] != name+" issue" || body["type"] != "standard" || body["description"] != "" {
				t.Errorf("issue type body: %v", body)
			}
			issueType = map[string]any{"id": "10001", "name": body["name"], "description": "", "subtask": false, "hierarchyLevel": 0, "avatarId": 100}
			write(issueType)
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issuetype/10001":
			if issueType == nil {
				w.WriteHeader(404)
				return
			}
			write(issueType)
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issuetypescheme":
			var body struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Default     string   `json:"defaultIssueTypeId"`
				IDs         []string `json:"issueTypeIds"`
			}
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Name != name+" scheme" || body.Description != "" || body.Default != "10001" || len(body.IDs) != 1 || body.IDs[0] != "10001" || issueType == nil {
				t.Errorf("scheme body: %+v", body)
			}
			scheme = map[string]any{"id": "10002", "name": body.Name, "description": "", "defaultIssueTypeId": "10001"}
			write(map[string]string{"issueTypeSchemeId": "10002"})
		case r.Method == "PUT" && r.URL.Path == "/rest/api/3/issuetypescheme/project":
			body := decode()
			if len(body) != 2 || body["projectId"] != "10500" || project == nil {
				t.Errorf("association body: %v", body)
			}
			switch body["issueTypeSchemeId"] {
			case "10002":
				if scheme == nil {
					t.Error("association before scheme")
				}
				assigned = true
			case "10000":
				if request != nil {
					t.Error("association reverted before request deletion")
				}
				assigned = false
			default:
				t.Errorf("unexpected scheme assignment: %v", body)
			}
			w.WriteHeader(204)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/rest/api/3/issuetypescheme"):
			values := []any{}
			switch r.URL.Path {
			case "/rest/api/3/issuetypescheme":
				if scheme != nil {
					values = append(values, scheme)
				}
			case "/rest/api/3/issuetypescheme/project":
				if assigned {
					values = append(values, map[string]any{"issueTypeScheme": scheme, "projectIds": []string{"10500"}})
				}
			case "/rest/api/3/issuetypescheme/mapping":
				if scheme != nil {
					values = append(values, map[string]string{"issueTypeSchemeId": "10002", "issueTypeId": "10001"})
				}
			default:
				t.Errorf("unexpected scheme read: %s", r.URL)
			}
			write(map[string]any{"isLast": true, "values": values})
		case r.Method == "DELETE" && r.URL.Path == "/rest/api/3/issuetypescheme/10002":
			if assigned || request != nil {
				t.Error("scheme deleted before dependents")
			}
			scheme = nil
			w.WriteHeader(204)
		case r.Method == "DELETE" && r.URL.Path == "/rest/api/3/issuetype/10001":
			if scheme != nil || request != nil {
				t.Error("issue type deleted before dependents")
			}
			issueType = nil
			w.WriteHeader(204)
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/project":
			body := decode()
			if len(body) != 5 || body["key"] != key || body["name"] != name || body["projectTypeKey"] != "service_desk" || body["leadAccountId"] != "test-account-id" || body["assigneeType"] != "UNASSIGNED" {
				t.Errorf("project body: %v", body)
			}
			project = map[string]any{"id": "10500", "key": key, "name": name, "description": "", "projectTypeKey": "service_desk", "lead": map[string]string{"accountId": "test-account-id"}, "assigneeType": "UNASSIGNED"}
			write(map[string]any{"id": 10500, "key": key})
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/project/"+key:
			if project == nil {
				w.WriteHeader(404)
				return
			}
			write(project)
		case r.Method == "GET" && r.URL.Path == "/rest/servicedeskapi/servicedesk":
			if project == nil {
				t.Error("desk lookup ran before project creation")
			}
			if len(r.URL.Query()) != 2 || r.URL.Query().Get("limit") != "100" {
				t.Errorf("desk query: %s", r.URL)
			}
			switch r.URL.Query().Get("start") {
			case "0":
				write(map[string]any{"start": 0, "size": 1, "isLastPage": false, "values": []any{map[string]string{"id": "1", "projectId": "10000", "projectKey": "OTHER", "projectName": "Other"}}})
			case "1":
				deskAttempts++
				if deskAttempts == 1 {
					write(map[string]any{"start": 1, "size": 0, "isLastPage": true, "values": []any{}})
					return
				}
				write(map[string]any{"start": 1, "size": 1, "isLastPage": true, "values": []any{map[string]string{"id": "2", "projectId": "10500", "projectKey": key, "projectName": name}}})
			default:
				t.Errorf("desk offset: %s", r.URL)
			}
		case r.Method == "POST" && r.URL.Path == "/rest/servicedeskapi/servicedesk/2/requesttype":
			body := decode()
			if len(body) != 4 || body["name"] != name+" request" || body["issueTypeId"] != "10001" || body["description"] != "" || body["helpText"] != "" {
				t.Errorf("request body: %v", body)
			}
			if project == nil || deskAttempts < 2 || !assigned || issueType == nil {
				t.Error("request created before desk resolution and issue type assignment")
			}
			if r.Header.Get("X-ExperimentalApi") != "opt-in" {
				t.Error("missing experimental opt-in")
			}
			request = map[string]any{"id": "10", "serviceDeskId": "2", "issueTypeId": "10001", "name": body["name"], "description": "", "helpText": "", "portalId": "3", "groupIds": []string{}}
			write(map[string]string{"id": "10"})
		case r.Method == "GET" && r.URL.Path == "/rest/servicedeskapi/servicedesk/2/requesttype/10":
			if request == nil {
				w.WriteHeader(404)
				return
			}
			write(request)
		case r.Method == "DELETE" && r.URL.Path == "/rest/servicedeskapi/servicedesk/2/requesttype/10":
			if r.Header.Get("X-ExperimentalApi") != "opt-in" {
				t.Error("missing experimental opt-in")
			}
			request = nil
			w.WriteHeader(204)
		case r.Method == "DELETE" && r.URL.Path == "/rest/api/3/project/"+key:
			if request != nil {
				t.Error("project deleted before request type")
			}
			project = nil
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
resource "atlassian_jira_project" "test" {
 key = %q
 name = %q
 project_type_key = "service_desk"
 lead_account_id = "test-account-id"
}
resource "atlassian_jira_issue_type" "test" {
 name = %q
 type = "standard"
}
resource "atlassian_jira_issue_type_scheme" "test" {
 name = %q
 default_issue_type_id = atlassian_jira_issue_type.test.id
 issue_type_ids = [atlassian_jira_issue_type.test.id]
}
resource "atlassian_jira_project_issue_type_scheme" "test" {
 project_id = atlassian_jira_project.test.id
 issue_type_scheme_id = atlassian_jira_issue_type_scheme.test.id
}
data "atlassian_jsm_service_desk" "test" {
 project_id = atlassian_jira_project.test.id
}
resource "atlassian_jsm_request_type" "test" {
 service_desk_id = data.atlassian_jsm_service_desk.test.id
 issue_type_id = atlassian_jira_issue_type.test.id
 name = %q
 depends_on = [atlassian_jira_project_issue_type_scheme.test]
}
`, key, name, name+" issue", name+" scheme", name+" request")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mu.Lock()
			defer mu.Unlock()
			if project != nil || request != nil || scheme != nil || issueType != nil || assigned {
				return fmt.Errorf("managed objects remain")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("atlassian_jira_project.test", "id", "10500"),
				resource.TestCheckResourceAttr("data.atlassian_jsm_service_desk.test", "id", "2"),
				resource.TestCheckResourceAttr("data.atlassian_jsm_service_desk.test", "project_id", "10500"),
				resource.TestCheckResourceAttr("data.atlassian_jsm_service_desk.test", "project_key", key),
				resource.TestCheckResourceAttr("data.atlassian_jsm_service_desk.test", "project_name", name),
				resource.TestCheckResourceAttr("atlassian_jsm_request_type.test", "service_desk_id", "2"),
			)},
			{Config: config, PlanOnly: true},
		}})
}
