package jsm

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRequestTypeOmittedEmptyMetadata(t *testing.T) {
	model := testModel()
	model.Description = types.StringValue("")
	model.HelpText = types.StringValue("")
	result := &requestTypeAPI{ID: "10", ServiceDeskID: "2", IssueTypeID: "10001", Name: "Original", PortalID: "3"}
	if err := setRequestTypeModel(context.Background(), &model, result); err != nil {
		t.Fatal(err)
	}
	if model.Description.IsNull() || model.Description.ValueString() != "" || model.HelpText.IsNull() || model.HelpText.ValueString() != "" {
		t.Fatal("omitted empty metadata not normalized")
	}
}
