package jira_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

type contextOptionMock struct {
	mu                   sync.Mutex
	context              map[string]any
	projects, issueTypes []string
	options              map[string]map[string]any
	next                 int
}

func newContextOptionMock(t *testing.T) (*contextOptionMock, *httptest.Server) {
	t.Helper()
	m := &contextOptionMock{options: map[string]map[string]any{"900": {"id": "900", "value": "Unmanaged", "disabled": false}}, next: 200}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		base := "/rest/api/3/field/customfield_10000/context"
		fail := func(message string) {
			t.Errorf("%s %s: %s", r.Method, r.URL, message)
			http.Error(w, message, http.StatusBadRequest)
		}
		decode := func(v any) bool {
			d := json.NewDecoder(r.Body)
			d.DisallowUnknownFields()
			if err := d.Decode(v); err != nil {
				fail(err.Error())
				return false
			}
			return true
		}
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		page := func(values []map[string]any) {
			q := r.URL.Query()
			if q.Get("maxResults") != "100" {
				fail("expected maxResults=100")
				return
			}
			start, err := strconv.Atoi(q.Get("startAt"))
			if err != nil || start < 0 {
				fail("missing/invalid startAt")
				return
			}
			total := len(values)
			// Deliberately cap to one value: clients must follow pagination.
			end := start + 1
			if end > total {
				end = total
			}
			if start > total {
				start = total
			}
			write(map[string]any{"startAt": start, "maxResults": 1, "total": total, "isLast": end >= total, "values": values[start:end]})
		}
		switch {
		case r.Method == "POST" && r.URL.Path == base:
			var body struct {
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Projects    []string `json:"projectIds"`
				IssueTypes  []string `json:"issueTypeIds"`
			}
			if !decode(&body) {
				return
			}
			if body.Name == "" || body.Projects == nil || body.IssueTypes == nil {
				fail("missing context name or explicit scopes")
				return
			}
			m.projects, m.issueTypes = body.Projects, body.IssueTypes
			m.context = map[string]any{"id": "100", "name": body.Name, "description": body.Description, "isGlobalContext": len(body.Projects) == 0, "isAnyIssueType": len(body.IssueTypes) == 0}
			w.WriteHeader(http.StatusCreated)
			write(map[string]any{"id": "100"}) // Deliberately partial create response.
		case r.Method == "GET" && r.URL.Path == base:
			if id := r.URL.Query().Get("contextId"); id != "" && id != "100" {
				fail("incorrect context filter")
				return
			}
			values := []map[string]any{}
			if r.URL.Query().Get("contextId") == "" {
				values = append(values, map[string]any{"id": "999", "name": "Other context", "description": "", "isGlobalContext": true, "isAnyIssueType": true})
			}
			if m.context != nil {
				values = append(values, m.context)
			}
			page(values)
		case r.Method == "GET" && (r.URL.Path == base+"/projectmapping" || r.URL.Path == base+"/issuetypemapping"):
			if r.URL.Query().Get("contextId") != "100" {
				fail("missing context filter")
				return
			}
			values := []map[string]any{}
			ids, key := m.projects, "projectId"
			if strings.HasSuffix(r.URL.Path, "issuetypemapping") {
				ids, key = m.issueTypes, "issueTypeId"
			}
			for _, id := range ids {
				values = append(values, map[string]any{"contextId": "100", key: id})
			}
			page(values)
		case r.Method == "PUT" && r.URL.Path == base+"/100":
			var body struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			}
			if !decode(&body) {
				return
			}
			if m.context == nil {
				w.WriteHeader(404)
				return
			}
			m.context["name"], m.context["description"] = body.Name, body.Description
			w.WriteHeader(204)
		case r.Method == "DELETE" && r.URL.Path == base+"/100":
			if m.context != nil && m.context["isGlobalContext"] == true {
				http.Error(w, "Global contexts cannot be deleted", http.StatusBadRequest)
				return
			}
			for id := range m.options {
				if id != "900" {
					fail("context deleted before managed options")
					return
				}
			}
			if m.context == nil {
				w.WriteHeader(404)
				return
			}
			m.context = nil
			w.WriteHeader(204)
		case r.Method == "GET" && r.URL.Path == base+"/100/option":
			if r.URL.Query().Get("optionId") != "" {
				fail("optionId filters children, not a single option")
				return
			}
			ids := []string{}
			for id := range m.options {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			values := []map[string]any{}
			for _, id := range ids {
				values = append(values, m.options[id])
			}
			page(values)
		case r.Method == "POST" && r.URL.Path == base+"/100/option":
			var body struct {
				Options []struct {
					Value    string `json:"value"`
					Disabled bool   `json:"disabled"`
					Parent   string `json:"optionId"`
				} `json:"options"`
			}
			if !decode(&body) {
				return
			}
			if len(body.Options) != 1 {
				fail("expected one option")
				return
			}
			o := body.Options[0]
			if o.Value == "" {
				fail("empty value")
				return
			}
			if o.Parent != "" {
				p := m.options[o.Parent]
				if p == nil || p["optionId"] != nil {
					fail("invalid/missing parent")
					return
				}
			}
			id := strconv.Itoa(m.next)
			m.next++
			result := map[string]any{"id": id, "value": o.Value, "disabled": o.Disabled}
			if o.Parent != "" {
				result["optionId"] = o.Parent
			}
			m.options[id] = result
			write(map[string]any{"options": []map[string]any{{"id": id}}})
		case r.Method == "PUT" && r.URL.Path == base+"/100/option":
			var body struct {
				Options []struct {
					ID       string `json:"id"`
					Value    string `json:"value"`
					Disabled bool   `json:"disabled"`
				} `json:"options"`
			}
			if !decode(&body) {
				return
			}
			if len(body.Options) != 1 {
				fail("expected one option update")
				return
			}
			o := body.Options[0]
			option := m.options[o.ID]
			if option == nil {
				w.WriteHeader(404)
				return
			}
			option["value"], option["disabled"] = o.Value, o.Disabled
			write(map[string]any{"options": []any{}}) // Jira omits unchanged options.
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, base+"/100/option/"):
			id := strings.TrimPrefix(r.URL.Path, base+"/100/option/")
			for _, o := range m.options {
				if o["optionId"] == id {
					fail("parent deleted before child")
					return
				}
			}
			if m.options[id] == nil {
				w.WriteHeader(404)
				return
			}
			delete(m.options, id)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("ATLASSIAN_URL", server.URL)
	t.Setenv("ATLASSIAN_USER", "test@example.org")
	t.Setenv("ATLASSIAN_TOKEN", "test-token")
	return m, server
}

func TestCustomFieldContextScopeReplacement(t *testing.T) {
	mock, _ := newContextOptionMock(t)
	name := "tf-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(scope string) string {
		return fmt.Sprintf(`
resource "atlassian_jira_custom_field_context" "test" {
 field_id = "customfield_10000"
 name = %q
 %s
}
`, name, scope)
	}
	original := config(`project_ids = ["10001"]`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if mock.context != nil {
				return fmt.Errorf("context still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: original},
			{Config: config(`project_ids = ["10002"]`), PlanOnly: true, ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jira_custom_field_context.test", plancheck.ResourceActionDestroyBeforeCreate)}}},
			{Config: config(`project_ids = ["10001"]` + "\n" + `issue_type_ids = ["10010"]`), PlanOnly: true, ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jira_custom_field_context.test", plancheck.ResourceActionDestroyBeforeCreate)}}},
			{Config: original, PlanOnly: true},
		},
	})
}

func TestCustomFieldContextDataSourceValidation(t *testing.T) {
	for _, tc := range []struct{ name, selectors, message string }{
		{"neither", "", "Invalid Attribute Combination"},
		{"both", `id = "100"` + "\n" + `name = "Named"`, "Invalid Attribute Combination"},
		{"not_found", `name = "Absent"`, "Custom field context not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newContextOptionMock(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
				Steps: []resource.TestStep{{Config: fmt.Sprintf(`
data "atlassian_jira_custom_field_context" "test" {
 field_id = "customfield_10000"
 %s
}
`, tc.selectors), ExpectError: regexp.MustCompile(tc.message)}},
			})
		})
	}
}

func TestCustomFieldOptionDataSourceNotFound(t *testing.T) {
	newContextOptionMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{Config: `
data "atlassian_jira_custom_field_option" "test" {
 field_id = "customfield_10000"
 context_id = "100"
 id = "missing"
}
`, ExpectError: regexp.MustCompile("Custom field option not found")}},
	})
}

func TestCustomFieldCascadingMap(t *testing.T) {
	mock, _ := newContextOptionMock(t)
	name := "tf-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
resource "atlassian_jira_custom_field_context" "test" {
 field_id = "customfield_10000"
 name = %q
 project_ids = ["10001"]
}
locals {
 departments = {
  "A/B" = ["Common", "Unit/One"]
  "A" = ["B/Common", "Common"]
  "Empty" = []
 }
 units = flatten([
  for department, units in local.departments : [
   for unit in units : { department = department, unit = unit }
  ]
 ])
 unit_map = { for entry in local.units : jsonencode([entry.department, entry.unit]) => entry }
}
# The testing library's legacy state shim cannot handle for_each resources.
# Use count here while exercising the same collision-safe map expressions.
resource "atlassian_jira_custom_field_option" "department" {
 count = length(local.departments)
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 value = keys(local.departments)[count.index]
}
resource "atlassian_jira_custom_field_option" "unit" {
 count = length(local.unit_map)
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 parent_option_id = atlassian_jira_custom_field_option.department[index(keys(local.departments), values(local.unit_map)[count.index].department)].id
 value = values(local.unit_map)[count.index].unit
}
`, name)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			mock.mu.Lock()
			defer mock.mu.Unlock()
			if mock.context != nil || len(mock.options) != 1 {
				return fmt.Errorf("managed objects not deleted")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: func(_ *terraform.State) error {
				mock.mu.Lock()
				defer mock.mu.Unlock()
				parents, children := 0, 0
				for id, option := range mock.options {
					if id == "900" {
						continue
					}
					if option["optionId"] == nil {
						parents++
					} else {
						children++
					}
				}
				if parents != 3 || children != 4 {
					return fmt.Errorf("parents=%d children=%d", parents, children)
				}
				return nil
			}},
			{Config: config, PlanOnly: true},
		},
	})
}

func TestCustomFieldContextOptionsLifecycle(t *testing.T) {
	for _, scoped := range []bool{false, true} {
		t.Run(fmt.Sprintf("scoped=%t", scoped), func(t *testing.T) {
			mock, _ := newContextOptionMock(t)
			name := "tf-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
			config := func(updated bool) string {
				scope := ""
				if scoped {
					scope = `project_ids = ["10001", "10002"]
 issue_type_ids = ["10010", "10011"]`
				}
				suffix := ""
				if updated {
					suffix = " updated"
				}
				return fmt.Sprintf(`
resource "atlassian_jira_custom_field_context" "test" {
 field_id = "customfield_10000"
 name = %q
 description = %q
 %s
}
resource "atlassian_jira_custom_field_option" "parent" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 value = %q
 disabled = %t
}
resource "atlassian_jira_custom_field_option" "child" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 parent_option_id = atlassian_jira_custom_field_option.parent.id
 value = %q
 disabled = %t
}
data "atlassian_jira_custom_field_context" "by_name" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 name = atlassian_jira_custom_field_context.test.name
}
data "atlassian_jira_custom_field_context" "by_id" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 id = atlassian_jira_custom_field_context.test.id
}
data "atlassian_jira_custom_field_option" "child" {
 field_id = atlassian_jira_custom_field_option.child.field_id
 context_id = atlassian_jira_custom_field_option.child.context_id
 id = atlassian_jira_custom_field_option.child.id
}
`, name+suffix, "description"+suffix, scope, "Department"+suffix, updated, "Unit"+suffix, updated)
			}
			checkDestroyed := func(_ *terraform.State) error {
				mock.mu.Lock()
				defer mock.mu.Unlock()
				if (scoped && mock.context != nil) || (!scoped && mock.context == nil) || len(mock.options) != 1 || mock.options["900"] == nil {
					return fmt.Errorf("managed resources remain or unmanaged option was deleted")
				}
				return nil
			}
			check := func(updated bool) resource.TestCheckFunc {
				suffix := ""
				if updated {
					suffix = " updated"
				}
				count := "0"
				if scoped {
					count = "2"
				}
				return resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "id", "100"),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "name", name+suffix),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "description", "description"+suffix),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "project_ids.#", count),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "issue_type_ids.#", count),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_context.by_name", "id", "100"),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_context.by_id", "name", name+suffix),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_context.by_id", "project_ids.#", count),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_context.by_id", "issue_type_ids.#", count),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_option.parent", "id", "200"),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_option.child", "parent_option_id", "200"),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_option.child", "value", "Unit"+suffix),
					resource.TestCheckResourceAttr("atlassian_jira_custom_field_option.child", "disabled", strconv.FormatBool(updated)),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_option.child", "value", "Unit"+suffix),
					resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_option.child", "parent_option_id", "200"),
				)
			}
			steps := []resource.TestStep{
				{Config: config(false), Check: check(false)},
				{ResourceName: "atlassian_jira_custom_field_context.test", ImportState: true, ImportStateId: "customfield_10000/100", ImportStateVerify: true},
				{ResourceName: "atlassian_jira_custom_field_option.parent", ImportState: true, ImportStateId: "customfield_10000/100/200", ImportStateVerify: true},
				{ResourceName: "atlassian_jira_custom_field_option.child", ImportState: true, ImportStateId: "customfield_10000/100/201", ImportStateVerify: true},
				{Config: config(true), Check: check(true)},
				{Config: strings.Replace(config(true), "parent_option_id = atlassian_jira_custom_field_option.parent.id", "parent_option_id = null", 1), PlanOnly: true, ExpectNonEmptyPlan: true,
					ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("atlassian_jira_custom_field_option.child", plancheck.ResourceActionDestroyBeforeCreate)}}},
				{Config: config(true), PlanOnly: true},
			}
			if !scoped {
				steps = append(steps,
					resource.TestStep{Config: config(true), Destroy: true, PlanOnly: true, ExpectError: regexp.MustCompile("Global context cannot be destroyed or replaced")},
					resource.TestStep{Config: strings.Replace(config(true), `description = `, "project_ids = [\"10001\"]\n description = ", 1), PlanOnly: true, ExpectError: regexp.MustCompile("Global context cannot be destroyed or replaced")},
					// Retain the non-deletable global context while tearing down options.
					resource.TestStep{Config: `removed {
 from = atlassian_jira_custom_field_context.test
 lifecycle { destroy = false }
}`},
				)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories, CheckDestroy: checkDestroyed, Steps: steps})
		})
	}
}
