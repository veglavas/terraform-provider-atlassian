package jira

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_ resource.Resource                = &fieldSchemeRequirementResource{}
	_ resource.ResourceWithImportState = &fieldSchemeRequirementResource{}
)

// NewFieldSchemeRequirementResource manages one issue-type-specific requirement.
func NewFieldSchemeRequirementResource() resource.Resource { return &fieldSchemeRequirementResource{} }

type fieldSchemeRequirementResource struct{ client *atlassian.Client }
type fieldSchemeRequirementModel struct {
	ID          types.String `tfsdk:"id"`
	SchemeID    types.String `tfsdk:"scheme_id"`
	FieldID     types.String `tfsdk:"field_id"`
	IssueTypeID types.String `tfsdk:"issue_type_id"`
	IsRequired  types.Bool   `tfsdk:"is_required"`
}

func (m fieldSchemeRequirementModel) key() fieldRequirementKey {
	return fieldRequirementKey{m.SchemeID.ValueString(), m.FieldID.ValueString(), m.IssueTypeID.ValueString()}
}
func (k fieldRequirementKey) id() string { return k.SchemeID + ":" + k.FieldID + ":" + k.IssueTypeID }

// The public API uses signed int64 IDs in request bodies. Reject aliases,
// overflow and leading zeros before an HTTP call or import is attempted.
type fieldSchemeIDValidator struct{}

func (fieldSchemeIDValidator) Description(context.Context) string {
	return "Must be a canonical positive int64 ID."
}
func (v fieldSchemeIDValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (fieldSchemeIDValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := positiveID(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid numeric ID", err.Error())
	}
}

var fieldSchemeFieldIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

func (r *fieldSchemeRequirementResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme_requirement"
}
func (r *fieldSchemeRequirementResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the required flag for one field and issue type in an existing Jira field association scheme using the experimental Field schemes API. The field must already be associated with the scheme and available to the issue type. Does not manage screens, field associations, or native JSM request-form field selection. Destroy removes only this issue type's isRequired override, restoring inheritance from the scheme default; it does not restore a previous override. Schemes may be shared by multiple projects.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Computed: true, Description: "Composite ID: SCHEME_ID:FIELD_ID:ISSUE_TYPE_ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"scheme_id":     schema.StringAttribute{Required: true, Description: "Existing field association scheme ID. All projects sharing this scheme are affected. Changing this requires replacement.", Validators: []validator.String{fieldSchemeIDValidator{}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"field_id":      schema.StringAttribute{Required: true, Description: "Existing associated Jira field ID, such as customfield_10000. Changing this requires replacement.", Validators: []validator.String{stringvalidator.RegexMatches(fieldSchemeFieldIDPattern, "must be a Jira field ID containing only letters, digits, and underscores")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"issue_type_id": schema.StringAttribute{Required: true, Description: "Issue type (work type) ID whose required flag is managed. Other issue types and scheme defaults are untouched. Changing this requires replacement.", Validators: []validator.String{fieldSchemeIDValidator{}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"is_required":   schema.BoolAttribute{Required: true, Description: "Whether this field must be supplied for this issue type. Reads the effective value, falling back to the scheme default when no required override is returned."},
		},
	}
}
func (r *fieldSchemeRequirementResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *atlassian.Client, got: %T", req.ProviderData))
		return
	}
	r.client = client
}
func (r *fieldSchemeRequirementResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fieldSchemeRequirementModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	required := plan.IsRequired.ValueBool()
	if err := writeFieldRequirement(ctx, r.client, plan.key(), &required); err != nil {
		resp.Diagnostics.AddError("Error setting field requirement", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.key().id())
	// Retain identity if post-write visibility fails; retrying the PUT is idempotent.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying field requirement", err.Error())
	}
}
func (r *fieldSchemeRequirementResource) wait(ctx context.Context, model fieldSchemeRequirementModel) error {
	// Verify the effective value, not explicit-override visibility. If the
	// default already matches, the successful write result is our acknowledgement.
	return waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		required, found, err := readFieldRequirement(ctx, r.client, model.key())
		return found && required == model.IsRequired.ValueBool(), err
	})
}
func (r *fieldSchemeRequirementResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fieldSchemeRequirementModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	required, found, err := readFieldRequirement(ctx, r.client, state.key())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field requirement", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	state.IsRequired = types.BoolValue(required)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *fieldSchemeRequirementResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan fieldSchemeRequirementModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	required := plan.IsRequired.ValueBool()
	if err := writeFieldRequirement(ctx, r.client, plan.key(), &required); err != nil {
		resp.Diagnostics.AddError("Error updating field requirement", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.key().id())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying field requirement", err.Error())
	}
}
func (r *fieldSchemeRequirementResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fieldSchemeRequirementModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, found, err := readFieldRequirement(ctx, r.client, state.key())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field requirement before removal", err.Error())
		return
	}
	if !found {
		return
	}
	if err := writeFieldRequirement(ctx, r.client, state.key(), nil); err != nil {
		// A parent/association can disappear between the read and bulk DELETE. A
		// bare 404 at the bulk endpoint is not sufficient evidence of parent deletion.
		_, stillFound, readErr := readFieldRequirement(ctx, r.client, state.key())
		if readErr == nil && !stillFound {
			return
		}
		resp.Diagnostics.AddError("Error removing field requirement override", err.Error())
	}
}
func (r *fieldSchemeRequirementResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, ":")
	if len(parts) != 3 {
		resp.Diagnostics.AddError("Invalid import ID", "Expected SCHEME_ID:FIELD_ID:ISSUE_TYPE_ID.")
		return
	}
	if _, err := positiveID(parts[0]); err != nil {
		resp.Diagnostics.AddError("Invalid scheme ID", err.Error())
		return
	}
	if _, err := positiveID(parts[2]); err != nil {
		resp.Diagnostics.AddError("Invalid issue type ID", err.Error())
		return
	}
	if !fieldSchemeFieldIDPattern.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid field ID", "Expected a Jira field ID containing only letters, digits, and underscores.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scheme_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("field_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("issue_type_id"), parts[2])...)
}
