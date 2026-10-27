package jira

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestFieldContextGlobalDestructivePlans(t *testing.T) {
	ctx := context.Background()
	r := &customFieldContextResource{}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	for _, tc := range []struct {
		name                               string
		change                             func(*customFieldContextModel)
		destroy, scoped, create, wantError bool
	}{
		{name: "global destroy", destroy: true, wantError: true},
		{name: "global project scope", change: func(m *customFieldContextModel) {
			m.ProjectIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10001")})
		}, wantError: true},
		{name: "global issue scope", change: func(m *customFieldContextModel) {
			m.IssueTypeIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10010")})
		}, wantError: true},
		{name: "global field change", change: func(m *customFieldContextModel) { m.FieldID = types.StringValue("customfield_10001") }, wantError: true},
		{name: "unknown field after rename", change: func(m *customFieldContextModel) { m.FieldID = types.StringUnknown() }},
		{name: "unknown project scope", change: func(m *customFieldContextModel) { m.ProjectIDs = types.SetUnknown(types.StringType) }},
		{name: "unknown issue scope", change: func(m *customFieldContextModel) { m.IssueTypeIDs = types.SetUnknown(types.StringType) }},
		{name: "unknown project in scope", change: func(m *customFieldContextModel) {
			m.ProjectIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()})
		}},
		{name: "unknown issue type in scope", change: func(m *customFieldContextModel) {
			m.IssueTypeIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()})
		}},
		{name: "known scope change with unknown field", change: func(m *customFieldContextModel) {
			m.FieldID = types.StringUnknown()
			m.ProjectIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10001")})
		}, wantError: true},
		{name: "metadata update", change: func(m *customFieldContextModel) {
			m.Name = types.StringValue("Renamed")
			m.Description = types.StringValue("Changed")
		}},
		{name: "stable global"},
		{name: "scoped destroy", scoped: true, destroy: true},
		{name: "new global", create: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := contextTestModel()
			if tc.scoped {
				model.ProjectIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("10001")})
			}
			state := tfsdk.State{Schema: schema.Schema}
			if tc.create {
				state.Raw = tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil)
			} else if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			if tc.change != nil {
				tc.change(&model)
			}
			plan := tfsdk.Plan{Schema: schema.Schema}
			if tc.destroy {
				plan.Raw = tftypes.NewValue(schema.Schema.Type().TerraformType(ctx), nil)
			} else if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
		})
	}
}
