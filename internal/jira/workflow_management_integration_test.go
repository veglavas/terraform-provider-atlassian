package jira_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// This test creates a disposable project and a real issue. The explicitly
// supplied fallback scheme is used for safe teardown; never assume ID 10000.
func TestIntegrationWorkflowManagement_populatedProject(t *testing.T) {
	testutil.SkipIfNoAcc(t)
	fallback := os.Getenv("ATLASSIAN_TEST_FALLBACK_SCHEME_ID")
	if fallback == "" {
		t.Skip("set ATLASSIAN_TEST_FALLBACK_SCHEME_ID to a disposable-compatible fallback scheme")
	}
	client, err := testutil.SweepClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	lead, err := getTestAccountID(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	key := "TF" + acctest.RandStringFromCharSet(7, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	ids := map[string]string{}
	var issueID string
	remember := func(s *terraform.State) error {
		for _, address := range []string{"atlassian_jira_workflow.main", "atlassian_jira_workflow.alternate", "atlassian_jira_workflow_scheme.main", "atlassian_jira_project.test"} {
			rs := s.RootModule().Resources[address]
			if rs == nil {
				return fmt.Errorf("missing %s", address)
			}
			id := rs.Primary.Attributes["id"]
			if previous := ids[address]; previous != "" && previous != id {
				return fmt.Errorf("%s replaced: %s -> %s", address, previous, id)
			}
			ids[address] = id
		}
		return nil
	}
	createIssue := func(s *terraform.State) error {
		if err := remember(s); err != nil {
			return err
		}
		projectID := ids["atlassian_jira_project.test"]
		var project struct {
			IssueTypes []struct {
				ID      string `json:"id"`
				Subtask bool   `json:"subtask"`
			} `json:"issueTypes"`
		}
		if err := client.Get(ctx, "/rest/api/3/project/"+atlassian.PathEscape(projectID), &project); err != nil {
			return err
		}
		issueType := ""
		for _, it := range project.IssueTypes {
			if !it.Subtask {
				issueType = it.ID
				break
			}
		}
		if issueType == "" {
			return fmt.Errorf("no standard issue type on disposable project")
		}
		var result struct {
			ID string `json:"id"`
		}
		err := client.Post(ctx, "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]string{"id": projectID}, "issuetype": map[string]string{"id": issueType}, "summary": "Terraform workflow migration acceptance test"}}, &result)
		issueID = result.ID
		return err
	}
	verifyIssue := func(s *terraform.State) error {
		if err := remember(s); err != nil {
			return err
		}
		var issue map[string]any
		return client.Get(ctx, "/rest/api/3/issue/"+atlassian.PathEscape(issueID), &issue)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			for address, id := range ids {
				if address == "atlassian_jira_project.test" {
					var p map[string]any
					status, err := client.GetWithStatus(ctx, "/rest/api/3/project/"+atlassian.PathEscape(id), &p)
					if err != nil {
						return err
					}
					if status != 404 {
						return fmt.Errorf("disposable project still exists")
					}
				}
				if address == "atlassian_jira_workflow.main" || address == "atlassian_jira_workflow.alternate" {
					var response struct {
						Workflows []any `json:"workflows"`
					}
					if err := client.Post(ctx, "/rest/api/3/workflows", map[string]any{"workflowIds": []string{id}}, &response); err != nil {
						return err
					}
					if len(response.Workflows) > 0 {
						return fmt.Errorf("workflow %s still exists", id)
					}
				}
			}
			return testCheckWorkflowSchemeDestroyed(s)
		},
		Steps: []resource.TestStep{
			{Config: workflowManagementConfig(name, key, lead, fallback, 1), Check: createIssue},
			{Config: workflowManagementConfig(name, key, lead, fallback, 2), Check: verifyIssue},
			{Config: workflowManagementConfig(name, key, lead, fallback, 3), Check: verifyIssue},
			{Config: workflowManagementConfig(name, key, lead, fallback, 3), PlanOnly: true},
		},
	})
}

func workflowManagementConfig(name, key, lead, fallback string, step int) string {
	defaultWorkflow := "atlassian_jira_workflow.main.name"
	association := "atlassian_jira_workflow_scheme.main.id"
	if step >= 2 {
		defaultWorkflow = "atlassian_jira_workflow.alternate.name"
	}
	if step >= 3 {
		association = "atlassian_jira_workflow_scheme.alternate.id"
	}
	return fmt.Sprintf(`
resource "atlassian_jira_project" "test" {
 key = %q
 name = %q
 project_type_key = "software"
 lead_account_id = %q
}
resource "atlassian_jira_workflow" "main" {
 name = %q
 description = "Active workflow update step %d"
 statuses = ["1"]
 transitions = {
  "1" = { name = "Create", type = "INITIAL", to_status_id = "1" }
  "11" = { name = "Reopen step %d", type = "GLOBAL", to_status_id = "1" }
 }
}
resource "atlassian_jira_workflow" "alternate" {
 name = %q
 statuses = ["1"]
}
resource "atlassian_jira_workflow_scheme" "main" {
 name = %q
 default_workflow = %s
}
resource "atlassian_jira_workflow_scheme" "alternate" {
 name = %q
 default_workflow = atlassian_jira_workflow.alternate.name
}
resource "atlassian_jira_project_workflow_scheme" "test" {
 project_id = atlassian_jira_project.test.id
 workflow_scheme_id = %s
 delete_workflow_scheme_id = %q
}
`, key, name, lead, name+"-main", step, step, name+"-alternate", name+"-scheme", defaultWorkflow, name+"-alternate-scheme", association, fallback)
}
