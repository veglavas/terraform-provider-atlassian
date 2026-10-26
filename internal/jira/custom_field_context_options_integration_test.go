package jira_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// Uses a newly created field, never an existing organizational field. The
// project ID only scopes the disposable context; the project is not modified.
func TestIntegrationCustomFieldContextOptions(t *testing.T) {
	testutil.SkipIfNoAcc(t)
	projectID := os.Getenv("ATLASSIAN_TEST_PROJECT_ID")
	if projectID == "" {
		t.Skip("set ATLASSIAN_TEST_PROJECT_ID to a disposable-compatible project")
	}
	if _, err := testutil.SweepClient(); err != nil {
		t.Fatal(err)
	}
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := func(updated bool) string {
		suffix := ""
		if updated {
			suffix = " updated"
		}
		return fmt.Sprintf(`
resource "atlassian_jira_custom_field" "test" {
 name = %q
 type = "com.atlassian.jira.plugin.system.customfieldtypes:cascadingselect"
}
resource "atlassian_jira_custom_field_context" "test" {
 field_id = atlassian_jira_custom_field.test.id
 name = %q
 description = "Disposable integration context"
 project_ids = [%q]
}
resource "atlassian_jira_custom_field_option" "parent" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 value = %q
}
resource "atlassian_jira_custom_field_option" "child" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 context_id = atlassian_jira_custom_field_context.test.id
 parent_option_id = atlassian_jira_custom_field_option.parent.id
 value = %q
 disabled = %t
}
data "atlassian_jira_custom_field_context" "test" {
 field_id = atlassian_jira_custom_field_context.test.field_id
 id = atlassian_jira_custom_field_context.test.id
}
data "atlassian_jira_custom_field_option" "test" {
 field_id = atlassian_jira_custom_field_option.child.field_id
 context_id = atlassian_jira_custom_field_option.child.context_id
 id = atlassian_jira_custom_field_option.child.id
}
`, name, name+suffix, projectID, name+" parent"+suffix, name+" child"+suffix, updated)
	}
	importID := func(address string) resource.ImportStateIdFunc {
		return func(s *terraform.State) (string, error) {
			rs := s.RootModule().Resources[address]
			if rs == nil {
				return "", fmt.Errorf("missing %s", address)
			}
			attrs := rs.Primary.Attributes
			if rs.Type == "atlassian_jira_custom_field_context" {
				return attrs["field_id"] + "/" + attrs["id"], nil
			}
			return attrs["field_id"] + "/" + attrs["context_id"] + "/" + attrs["id"], nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             testCheckCustomFieldDestroyed,
		Steps: []resource.TestStep{
			{Config: config(false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("atlassian_jira_custom_field_context.test", "id"),
				resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_context.test", "project_ids.#", "1"),
				resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_option.test", "disabled", "false"),
				resource.TestCheckResourceAttrSet("data.atlassian_jira_custom_field_option.test", "parent_option_id"),
			)},
			{ResourceName: "atlassian_jira_custom_field_context.test", ImportState: true, ImportStateIdFunc: importID("atlassian_jira_custom_field_context.test"), ImportStateVerify: true},
			{ResourceName: "atlassian_jira_custom_field_option.parent", ImportState: true, ImportStateIdFunc: importID("atlassian_jira_custom_field_option.parent"), ImportStateVerify: true},
			{ResourceName: "atlassian_jira_custom_field_option.child", ImportState: true, ImportStateIdFunc: importID("atlassian_jira_custom_field_option.child"), ImportStateVerify: true},
			{Config: config(true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("atlassian_jira_custom_field_context.test", "name", name+" updated"),
				resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_option.test", "value", name+" child updated"),
				resource.TestCheckResourceAttr("data.atlassian_jira_custom_field_option.test", "disabled", "true"),
			)},
			{Config: config(true), PlanOnly: true},
		},
	})
}
