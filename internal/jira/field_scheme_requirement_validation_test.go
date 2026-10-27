package jira_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

func TestFieldSchemeRequirementValidation(t *testing.T) {
	for _, tc := range []struct{ name, scheme, field, issue string }{
		{"non numeric scheme", "SUPPORT", "customfield_10000", "10001"},
		{"non canonical scheme", "010000", "customfield_10000", "10001"},
		{"overflow issue type", "10000", "customfield_10000", "9223372036854775808"},
		{"invalid field", "10000", "bad/field", "10001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				t.Errorf("validation made an HTTP request: %s %s", r.Method, r.URL)
				w.WriteHeader(400)
			}))
			defer server.Close()
			t.Setenv("ATLASSIAN_URL", server.URL)
			t.Setenv("ATLASSIAN_USER", "test@example.org")
			t.Setenv("ATLASSIAN_TOKEN", "test")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
				CheckDestroy: func(_ *terraform.State) error {
					if calls.Load() != 0 {
						return fmt.Errorf("validation called API")
					}
					return nil
				},
				Steps: []resource.TestStep{{Config: fmt.Sprintf(`
resource "atlassian_jira_field_scheme_requirement" "test" {
 scheme_id = %q
 field_id = %q
 issue_type_id = %q
 is_required = true
}
`, tc.scheme, tc.field, tc.issue), ExpectError: regexp.MustCompile(`Invalid numeric ID|must be a Jira field ID`)}},
			})
			if calls.Load() != 0 {
				t.Fatal("validation called API")
			}
		})
	}
}
