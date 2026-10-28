package jira

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Values that were unknown during schema validation can resolve to invalid
// entries at apply time. Reject them before any ordering request is sent.
func TestScreenTabFieldOrderDesiredIDsApplyValidation(t *testing.T) {
	for name, values := range map[string][]attr.Value{
		"duplicate resolved IDs": {types.StringValue("summary"), types.StringValue("summary")},
		"empty resolved ID":      {types.StringValue("")},
		"null resolved ID":       {types.StringNull()},
		"unknown resolved ID":    {types.StringUnknown()},
	} {
		t.Run(name, func(t *testing.T) {
			ids := types.ListValueMust(types.StringType, values)
			_, diags := screenTabFieldOrderDesiredIDs(t.Context(), ids)
			if !diags.HasError() {
				t.Fatal("expected invalid apply-time field IDs to be rejected")
			}
		})
	}
}
