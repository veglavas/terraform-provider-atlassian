package jira

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ resource.ResourceWithImportState = &fieldSchemeFieldResource{}

func NewFieldSchemeFieldResource() resource.Resource { return &fieldSchemeFieldResource{} }

type fieldSchemeFieldResource struct{ client *atlassian.Client }
type fieldSchemeFieldModel struct {
	ID           types.String `tfsdk:"id"`
	SchemeID     types.String `tfsdk:"scheme_id"`
	FieldID      types.String `tfsdk:"field_id"`
	IssueTypeIDs types.Set    `tfsdk:"issue_type_ids"`
}

func (r *fieldSchemeFieldResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme_field"
}
func (r *fieldSchemeFieldResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages one field association in a Jira field scheme using the experimental API. Requires Administer Jira. Owns the complete issue-type restriction set for this field in this scheme. Does not manage screen placement or native portal visibility. Removing an association also removes its scheme parameters and issue-type overrides; destroy requirement resources first. Import existing associations to avoid unintentionally changing restrictions.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, Description: "SCHEME_ID:FIELD_ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"scheme_id":      schema.StringAttribute{Required: true, Description: "Field scheme ID.", Validators: []validator.String{fieldSchemeIDValidator{}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"field_id":       schema.StringAttribute{Required: true, Description: "Jira field ID, such as customfield_10000.", Validators: []validator.String{stringvalidator.RegexMatches(fieldSchemeFieldIDPattern, "must be a Jira field ID")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"issue_type_ids": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})), Description: "Complete set of issue types to which the field is restricted. Empty or omitted means all issue types; updating replaces the previous restriction set.", Validators: []validator.Set{setvalidator.ValueStringsAre(fieldSchemeIDValidator{})}},
		}}
}
func (r *fieldSchemeFieldResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}
func (r *fieldSchemeFieldResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fieldSchemeFieldModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ids []string
	resp.Diagnostics.Append(plan.IssueTypeIDs.ElementsAs(ctx, &ids, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := writeFieldSchemeAssociation(ctx, r.client, plan.SchemeID.ValueString(), plan.FieldID.ValueString(), ids, false); err != nil {
		resp.Diagnostics.AddError("Error associating field with scheme", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.SchemeID.ValueString() + ":" + plan.FieldID.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying field association", err.Error())
	}
}
func (r *fieldSchemeFieldResource) wait(ctx context.Context, model fieldSchemeFieldModel) error {
	return waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		f, err := readManagedFieldAssociation(ctx, r.client, model.SchemeID.ValueString(), model.FieldID.ValueString())
		if err != nil || f == nil {
			return false, err
		}
		set, d := types.SetValueFrom(ctx, types.StringType, nonNilStrings(f.RestrictedToWorkTypes))
		if d.HasError() {
			return false, fmt.Errorf("invalid issue type restrictions")
		}
		return set.Equal(model.IssueTypeIDs), nil
	})
}
func (r *fieldSchemeFieldResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fieldSchemeFieldModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f, err := readManagedFieldAssociation(ctx, r.client, state.SchemeID.ValueString(), state.FieldID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field association", err.Error())
		return
	}
	if f == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.IssueTypeIDs, _ = types.SetValueFrom(ctx, types.StringType, nonNilStrings(f.RestrictedToWorkTypes))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func nonNilStrings(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
func (r *fieldSchemeFieldResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan fieldSchemeFieldModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ids []string
	resp.Diagnostics.Append(plan.IssueTypeIDs.ElementsAs(ctx, &ids, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := writeFieldSchemeAssociation(ctx, r.client, plan.SchemeID.ValueString(), plan.FieldID.ValueString(), ids, false); err != nil {
		resp.Diagnostics.AddError("Error updating field association", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying field association", err.Error())
	}
}
func (r *fieldSchemeFieldResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fieldSchemeFieldModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f, err := readManagedFieldAssociation(ctx, r.client, state.SchemeID.ValueString(), state.FieldID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field association before removal", err.Error())
		return
	}
	if f == nil {
		return
	}
	if err := writeFieldSchemeAssociation(ctx, r.client, state.SchemeID.ValueString(), state.FieldID.ValueString(), nil, true); err != nil {
		f, readErr := readManagedFieldAssociation(ctx, r.client, state.SchemeID.ValueString(), state.FieldID.ValueString())
		if readErr == nil && f == nil {
			return
		}
		resp.Diagnostics.AddError("Error removing field association", err.Error())
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		f, err := readManagedFieldAssociation(ctx, r.client, state.SchemeID.ValueString(), state.FieldID.ValueString())
		return f == nil, err
	}); err != nil {
		resp.Diagnostics.AddError("Error verifying association removal", err.Error())
	}
}
func (r *fieldSchemeFieldResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, ":")
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid field association import ID", "Expected SCHEME_ID:FIELD_ID.")
		return
	}
	if _, err := positiveID(parts[0]); err != nil {
		resp.Diagnostics.AddError("Invalid scheme ID", err.Error())
		return
	}
	if !fieldSchemeFieldIDPattern.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid field ID", "Expected a Jira field ID.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scheme_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("field_id"), parts[1])...)
}
