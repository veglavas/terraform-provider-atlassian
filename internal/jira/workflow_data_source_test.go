package jira_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

func TestAccWorkflowDataSource_NotFound(t *testing.T) {
	for _, status := range []int{200, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/rest/api/3/workflows/search" || r.URL.Query().Get("queryString") != "NonExistentWorkflow" || r.URL.Query().Get("startAt") != "0" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(status)
				if status == 200 {
					fmt.Fprint(w, `{"values":[{"id":"other","name":"Other Workflow","statuses":[]}],"isLast":true}`)
				}
			}))
			defer server.Close()
			t.Setenv("ATLASSIAN_URL", server.URL)
			t.Setenv("ATLASSIAN_USER", "test@test.com")
			t.Setenv("ATLASSIAN_TOKEN", "test-token")
			resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories, Steps: []resource.TestStep{{Config: `data "atlassian_jira_workflow" "test" { name = "NonExistentWorkflow" }`, ExpectError: regexp.MustCompile("Workflow not found")}}})
		})
	}
}
