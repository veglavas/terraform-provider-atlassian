package jsm_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// Creates only a disposable request type in an explicitly supplied test project.
// Discovers its service desk without requiring a manually supplied desk ID.
// Never selects or modifies an existing request type by name.
func TestIntegrationRequestType(t *testing.T) {
	testutil.SkipIfNoAcc(t)
	projectID := os.Getenv("ATLASSIAN_TEST_PROJECT_ID")
	issueTypeID := os.Getenv("ATLASSIAN_TEST_REQUEST_ISSUE_TYPE_ID")
	if projectID == "" || issueTypeID == "" {
		t.Skip("set ATLASSIAN_TEST_PROJECT_ID and ATLASSIAN_TEST_REQUEST_ISSUE_TYPE_ID for a company-managed demo service project")
	}
	client, err := testutil.SweepClient()
	if err != nil {
		t.Fatal(err)
	}
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
data "atlassian_jsm_service_desk" "test" {
 project_id = %q
}
resource "atlassian_jsm_request_type" "test" {
 service_desk_id = data.atlassian_jsm_service_desk.test.id
 issue_type_id = %q
 name = %q
 description = "Disposable acceptance request type"
 help_text = "Acceptance test only"
}
data "atlassian_jsm_request_type" "test" {
 service_desk_id = atlassian_jsm_request_type.test.service_desk_id
 id = atlassian_jsm_request_type.test.id
}
`, projectID, issueTypeID, name)
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			for address, rs := range s.RootModule().Resources {
				if rs.Type != "atlassian_jsm_request_type" || strings.HasPrefix(address, "data.") {
					continue
				}
				var result map[string]any
				apiPath := "/rest/servicedeskapi/servicedesk/" + atlassian.PathEscape(rs.Primary.Attributes["service_desk_id"]) + "/requesttype/" + atlassian.PathEscape(rs.Primary.ID)
				status, err := client.GetWithStatus(context.Background(), apiPath, &result)
				if err != nil {
					return err
				}
				if status != http.StatusNotFound {
					return fmt.Errorf("request type %s still exists", rs.Primary.ID)
				}
			}
			return nil
		}, Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("atlassian_jsm_request_type.test", "id"),
				resource.TestCheckResourceAttrSet("atlassian_jsm_request_type.test", "portal_id"),
				resource.TestCheckResourceAttr("atlassian_jsm_request_type.test", "group_ids.#", "0"),
				resource.TestCheckResourceAttr("data.atlassian_jsm_request_type.test", "name", name),
			)},
			{ResourceName: "atlassian_jsm_request_type.test", ImportState: true, ImportStateIdFunc: func(s *terraform.State) (string, error) {
				rs := s.RootModule().Resources["atlassian_jsm_request_type.test"]
				if rs == nil {
					return "", fmt.Errorf("request type not in state")
				}
				return rs.Primary.Attributes["service_desk_id"] + "/" + rs.Primary.ID, nil
			}, ImportStateVerify: true},
			{Config: config, PlanOnly: true},
		}})
}
