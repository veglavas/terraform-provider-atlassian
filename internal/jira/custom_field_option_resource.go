package jira

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_ resource.Resource                = &customFieldOptionResource{}
	_ resource.ResourceWithImportState = &customFieldOptionResource{}
)

func NewCustomFieldOptionResource() resource.Resource { return &customFieldOptionResource{} }

type customFieldOptionResource struct{ client *atlassian.Client }

type customFieldOptionModel struct {
	ID             types.String `tfsdk:"id"`
	FieldID        types.String `tfsdk:"field_id"`
	ContextID      types.String `tfsdk:"context_id"`
	Value          types.String `tfsdk:"value"`
	Disabled       types.Bool   `tfsdk:"disabled"`
	ParentOptionID types.String `tfsdk:"parent_option_id"`
}

func (r *customFieldOptionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_custom_field_option"
}

func (r *customFieldOptionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	immutable := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, Description: description, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}}
	}
	resp.Schema = schema.Schema{
		Description: "Manages one option of a Jira custom field context, including cascading children. Other options are not managed. Requires Administer Jira. Prefer disabling options to deleting values used by existing issues. Ordering and app-owned options are not supported.",
		Attributes: map[string]schema.Attribute{
			"id":               schema.StringAttribute{Computed: true, Description: "The option ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"field_id":         immutable("The custom field ID. Changing this replaces the option."),
			"context_id":       immutable("The context ID. Changing this replaces the option."),
			"value":            schema.StringAttribute{Required: true, Description: "The option's display value.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"disabled":         schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Whether this option is disabled."},
			"parent_option_id": schema.StringAttribute{Optional: true, Description: "Parent option ID for a cascading child. Omit for a top-level option. Changing this replaces the option. Reference the parent resource ID to establish creation/deletion dependencies.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		},
	}
}

func (r *customFieldOptionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *customFieldOptionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan customFieldOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiPath := fieldContextPath(plan.FieldID.ValueString(), plan.ContextID.ValueString())
	unlock := lockFieldContext(r.client, apiPath)
	defer unlock()
	option := fieldOptionAPI{Value: plan.Value.ValueString(), Disabled: plan.Disabled.ValueBool(), ParentID: plan.ParentOptionID.ValueString()}
	var result struct {
		Options []fieldOptionAPI `json:"options"`
	}
	if err := r.client.Post(ctx, apiPath+"/option", map[string]any{"options": []fieldOptionAPI{option}}, &result); err != nil {
		resp.Diagnostics.AddError("Error creating custom field option", err.Error())
		return
	}
	if len(result.Options) != 1 || result.Options[0].ID == "" {
		resp.Diagnostics.AddError("Missing option ID", "Jira returned no unique option ID. The option may have been created; inspect Jira before retrying.")
		return
	}
	plan.ID = types.StringValue(result.Options[0].ID)
	// Persist the ID before waiting so a timeout does not orphan the option.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		option, err := findFieldOption(ctx, r.client, plan.FieldID.ValueString(), plan.ContextID.ValueString(), plan.ID.ValueString())
		return option != nil, err
	}); err != nil {
		resp.Diagnostics.AddError("Error waiting for custom field option", err.Error())
	}
}

func (r *customFieldOptionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state customFieldOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	option, err := findFieldOption(ctx, r.client, state.FieldID.ValueString(), state.ContextID.ValueString(), state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field option", err.Error())
		return
	}
	if option == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Value, state.Disabled = types.StringValue(option.Value), types.BoolValue(option.Disabled)
	state.ParentOptionID = types.StringNull()
	if option.ParentID != "" {
		state.ParentOptionID = types.StringValue(option.ParentID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *customFieldOptionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan customFieldOptionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiPath := fieldContextPath(plan.FieldID.ValueString(), plan.ContextID.ValueString())
	unlock := lockFieldContext(r.client, apiPath)
	defer unlock()
	// Parent IDs are creation-only in Jira's update contract. Unchanged options
	// may be omitted from the response, so do not infer deletion from that response.
	option := map[string]any{"id": plan.ID.ValueString(), "value": plan.Value.ValueString(), "disabled": plan.Disabled.ValueBool()}
	if err := r.client.Put(ctx, apiPath+"/option", map[string]any{"options": []any{option}}, nil); err != nil {
		resp.Diagnostics.AddError("Error updating custom field option", err.Error())
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		option, err := findFieldOption(ctx, r.client, plan.FieldID.ValueString(), plan.ContextID.ValueString(), plan.ID.ValueString())
		return option != nil && option.Value == plan.Value.ValueString() && option.Disabled == plan.Disabled.ValueBool(), err
	}); err != nil {
		resp.Diagnostics.AddError("Error waiting for custom field option update", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *customFieldOptionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state customFieldOptionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	apiPath := fieldContextPath(state.FieldID.ValueString(), state.ContextID.ValueString())
	unlock := lockFieldContext(r.client, apiPath)
	defer unlock()
	if _, err := r.client.DeleteWithStatus(ctx, apiPath+"/option/"+atlassian.PathEscape(state.ID.ValueString())); err != nil {
		resp.Diagnostics.AddError("Error deleting custom field option", err.Error())
	}
}

func (r *customFieldOptionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Expected {field_id}/{context_id}/{option_id}.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("field_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("context_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[2])...)
}
