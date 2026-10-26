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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_ resource.Resource                = &customFieldContextResource{}
	_ resource.ResourceWithImportState = &customFieldContextResource{}
	_ resource.ResourceWithModifyPlan  = &customFieldContextResource{}
)

func NewCustomFieldContextResource() resource.Resource { return &customFieldContextResource{} }

type customFieldContextResource struct{ client *atlassian.Client }

type customFieldContextModel struct {
	ID           types.String `tfsdk:"id"`
	FieldID      types.String `tfsdk:"field_id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	ProjectIDs   types.Set    `tfsdk:"project_ids"`
	IssueTypeIDs types.Set    `tfsdk:"issue_type_ids"`
}

func (r *customFieldContextResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_custom_field_context"
}

func (r *customFieldContextResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	scope := func(description string) schema.SetAttribute {
		return schema.SetAttribute{
			Description: description + " Changing scope replaces the context and may delete its options. Global contexts cannot be destroyed or replaced. Use prevent_destroy for existing contexts.",
			Optional:    true, Computed: true, ElementType: types.StringType,
			Default:       setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			Validators:    []validator.Set{setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1))},
			PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
		}
	}
	resp.Schema = schema.Schema{
		Description: "Manages a Jira custom field context. Requires Administer Jira. Import existing contexts instead of creating overlapping contexts. Deleting a context can delete its options and affect issue values.",
		Attributes: map[string]schema.Attribute{
			"id":             schema.StringAttribute{Computed: true, Description: "The context ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"field_id":       schema.StringAttribute{Required: true, Description: "The custom field ID. Changing this replaces the context.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":           schema.StringAttribute{Required: true, Description: "The context name.", Validators: []validator.String{stringvalidator.LengthBetween(1, 255)}},
			"description":    schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "The context description.", Validators: []validator.String{stringvalidator.LengthAtMost(255)}},
			"project_ids":    scope("Project IDs. An empty set creates a global context."),
			"issue_type_ids": scope("Issue type IDs. An empty set applies to all issue types."),
		},
	}
}

// Jira prohibits deleting global contexts. Reject destructive plans before
// Terraform can delete dependent options, rather than failing late in Delete.
func (r *customFieldContextResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		return
	}
	var state customFieldContextModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.ProjectIDs.IsNull() || state.ProjectIDs.IsUnknown() || len(state.ProjectIDs.Elements()) != 0 {
		return
	}
	destructive := req.Plan.Raw.IsNull()
	if !destructive {
		var plan customFieldContextModel
		resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
		if resp.Diagnostics.HasError() {
			return
		}
		destructive = !plan.FieldID.Equal(state.FieldID) || !plan.ProjectIDs.Equal(state.ProjectIDs) || !plan.IssueTypeIDs.Equal(state.IssueTypeIDs)
	}
	if destructive {
		resp.Diagnostics.AddError("Global context cannot be destroyed or replaced", "Jira prohibits deleting global custom field contexts. Keep its current field and scope. To stop managing it without deleting it, use a removed block with lifecycle { destroy = false } (Terraform 1.7+) or carefully remove only its state entry. Protect dependent options with prevent_destroy and review the entire plan; retaining a context does not retain options removed from configuration. Scope migration requires a separate, explicitly planned Jira migration.")
	}
}

func (r *customFieldContextResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *customFieldContextResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan customFieldContextModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := fieldContextWrite{Name: plan.Name.ValueString(), Description: plan.Description.ValueString(), ProjectIDs: []string{}, IssueTypeIDs: []string{}}
	resp.Diagnostics.Append(plan.ProjectIDs.ElementsAs(ctx, &body.ProjectIDs, false)...)
	resp.Diagnostics.Append(plan.IssueTypeIDs.ElementsAs(ctx, &body.IssueTypeIDs, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := r.client.Post(ctx, fieldContextsPath(plan.FieldID.ValueString()), body, &result); err != nil {
		resp.Diagnostics.AddError("Error creating custom field context", err.Error())
		return
	}
	if result.ID == "" {
		resp.Diagnostics.AddError("Missing context ID", "Jira returned no context ID. The context may have been created; inspect Jira before retrying.")
		return
	}
	plan.ID = types.StringValue(result.ID)
	// Persist the ID before waiting so a timeout does not orphan the context.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		c, err := findFieldContext(ctx, r.client, plan.FieldID.ValueString(), result.ID, "")
		return c != nil, err
	}); err != nil {
		resp.Diagnostics.AddError("Error waiting for custom field context", err.Error())
	}
}

func (r *customFieldContextResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state customFieldContextModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := findFieldContext(ctx, r.client, state.FieldID.ValueString(), state.ID.ValueString(), "")
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field context", err.Error())
		return
	}
	if c == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	projects, issueTypes, missing, err := readFieldContextScope(ctx, r.client, state.FieldID.ValueString(), *c)
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field context scope", err.Error())
		return
	}
	if missing {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Name, state.Description = types.StringValue(c.Name), types.StringValue(c.Description)
	projectSet, diags := types.SetValueFrom(ctx, types.StringType, projects)
	resp.Diagnostics.Append(diags...)
	issueTypeSet, diags := types.SetValueFrom(ctx, types.StringType, issueTypes)
	resp.Diagnostics.Append(diags...)
	state.ProjectIDs, state.IssueTypeIDs = projectSet, issueTypeSet
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
}

func (r *customFieldContextResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan customFieldContextModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]string{"name": plan.Name.ValueString(), "description": plan.Description.ValueString()}
	if err := r.client.Put(ctx, fieldContextPath(plan.FieldID.ValueString(), plan.ID.ValueString()), body, nil); err != nil {
		resp.Diagnostics.AddError("Error updating custom field context", err.Error())
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		c, err := findFieldContext(ctx, r.client, plan.FieldID.ValueString(), plan.ID.ValueString(), "")
		return c != nil && c.Name == plan.Name.ValueString() && c.Description == plan.Description.ValueString(), err
	}); err != nil {
		resp.Diagnostics.AddError("Error waiting for custom field context update", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *customFieldContextResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state customFieldContextModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.DeleteWithStatus(ctx, fieldContextPath(state.FieldID.ValueString(), state.ID.ValueString())); err != nil {
		resp.Diagnostics.AddError("Error deleting custom field context", err.Error())
	}
}

func (r *customFieldContextResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Expected {field_id}/{context_id}.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("field_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
