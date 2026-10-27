package jira

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ resource.ResourceWithImportState = &projectFieldSchemeResource{}

func NewProjectFieldSchemeResource() resource.Resource { return &projectFieldSchemeResource{} }

type projectFieldSchemeResource struct{ client *atlassian.Client }
type projectFieldSchemeModel struct {
	ProjectID         types.String `tfsdk:"project_id"`
	SchemeID          types.String `tfsdk:"scheme_id"`
	OnDestroySchemeID types.String `tfsdk:"on_destroy_scheme_id"`
}

func (r *projectFieldSchemeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_project_field_scheme"
}
func (r *projectFieldSchemeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a project's field scheme assignment using the experimental Field schemes API. Requires Administer Jira. Import by project ID. Jira exposes assignment, not unassignment: by default destroy retains the remote assignment and relinquishes Terraform ownership. Set on_destroy_scheme_id to explicitly reassign to a known existing fallback on destroy. There is no hardcoded default and no automatic restoration of an earlier scheme. An assigned scheme cannot be deleted until the project is reassigned or deleted.",
		Attributes: map[string]schema.Attribute{
			"project_id":           schema.StringAttribute{Required: true, Description: "Project ID; also the import ID. Changing it requires replacement.", Validators: []validator.String{fieldSchemeIDValidator{}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"scheme_id":            schema.StringAttribute{Required: true, Description: "Managed field scheme ID. Changes reassign the project in place.", Validators: []validator.String{fieldSchemeIDValidator{}}},
			"on_destroy_scheme_id": schema.StringAttribute{Optional: true, Description: "Explicit existing fallback scheme to assign on destroy. When omitted the assignment is retained, which can block deletion of the assigned scheme. This is local destroy policy, not a remotely readable attribute.", Validators: []validator.String{fieldSchemeIDValidator{}}},
		}}
}
func (r *projectFieldSchemeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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
func (r *projectFieldSchemeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectFieldSchemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := assignFieldSchemeProject(ctx, r.client, plan.SchemeID.ValueString(), plan.ProjectID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error assigning project field scheme", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan.ProjectID.ValueString(), plan.SchemeID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error verifying project field scheme", err.Error())
	}
}
func (r *projectFieldSchemeResource) wait(ctx context.Context, project, scheme string) error {
	return waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		id, err := readManagedProjectFieldScheme(ctx, r.client, project)
		return id == scheme, err
	})
}
func (r *projectFieldSchemeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectFieldSchemeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := readManagedProjectFieldScheme(ctx, r.client, state.ProjectID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading project field scheme", err.Error())
		return
	}
	if id == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.SchemeID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *projectFieldSchemeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectFieldSchemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Destroy policy is local state, not a reason to reassign the project.
	if plan.SchemeID.Equal(state.SchemeID) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}
	if err := assignFieldSchemeProject(ctx, r.client, plan.SchemeID.ValueString(), plan.ProjectID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error updating project field scheme", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan.ProjectID.ValueString(), plan.SchemeID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error verifying project field scheme", err.Error())
	}
}
func (r *projectFieldSchemeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectFieldSchemeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.OnDestroySchemeID.IsNull() {
		resp.Diagnostics.AddWarning("Project field scheme assignment retained", "Jira has no unassignment endpoint. Terraform relinquished ownership without changing the assignment; set on_destroy_scheme_id before destroy to explicitly reassign instead.")
		return
	}
	current, err := readManagedProjectFieldScheme(ctx, r.client, state.ProjectID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading project field scheme before reassignment", err.Error())
		return
	}
	if current == "" || current == state.OnDestroySchemeID.ValueString() {
		return
	}
	if current != state.SchemeID.ValueString() {
		resp.Diagnostics.AddWarning("Project field scheme assignment changed", "Assignment changed out of band; destroy did not overwrite it.")
		return
	}
	if err := assignFieldSchemeProject(ctx, r.client, state.OnDestroySchemeID.ValueString(), state.ProjectID.ValueString()); err != nil {
		current, readErr := readManagedProjectFieldScheme(ctx, r.client, state.ProjectID.ValueString())
		if readErr == nil && current == "" {
			return
		}
		resp.Diagnostics.AddError("Error reassigning project field scheme on destroy", err.Error())
		return
	}
	if err := r.wait(ctx, state.ProjectID.ValueString(), state.OnDestroySchemeID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error verifying fallback field scheme assignment", err.Error())
	}
}
func (r *projectFieldSchemeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := positiveID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid project import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), req.ID)...)
}
