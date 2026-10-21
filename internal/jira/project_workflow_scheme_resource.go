package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_ resource.Resource                = &projectWorkflowSchemeResource{}
	_ resource.ResourceWithImportState = &projectWorkflowSchemeResource{}
)

// NewProjectWorkflowSchemeResource returns a new project workflow scheme association resource.
func NewProjectWorkflowSchemeResource() resource.Resource {
	return &projectWorkflowSchemeResource{}
}

type projectWorkflowSchemeResource struct {
	client *atlassian.Client
}

type projectWorkflowSchemeResourceModel struct {
	ProjectID              types.String `tfsdk:"project_id"`
	WorkflowSchemeID       types.String `tfsdk:"workflow_scheme_id"`
	StatusMappings         types.Map    `tfsdk:"status_mappings"`
	DeleteWorkflowSchemeID types.String `tfsdk:"delete_workflow_scheme_id"`
	DeleteStatusMappings   types.Map    `tfsdk:"delete_status_mappings"`
}

// projectWorkflowSchemeListEntry represents a single entry in the
// GET /rest/api/3/workflowscheme/project response values array.
type projectWorkflowSchemeListEntry struct {
	WorkflowScheme workflowSchemeAPIResponse `json:"workflowScheme"`
}

func (r *projectWorkflowSchemeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_project_workflow_scheme"
}

func (r *projectWorkflowSchemeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Associates a Jira Cloud project with a workflow scheme.",
		Attributes: map[string]schema.Attribute{
			"status_mappings":           migrationMappingsSchema(),
			"delete_status_mappings":    migrationMappingsSchema(),
			"delete_workflow_scheme_id": schema.StringAttribute{Optional: true, Description: "Explicit scheme to switch to on destroy. If omitted, destroy only stops managing the association and leaves Jira unchanged."},
			"project_id": schema.StringAttribute{
				Description: "The ID of the project. Changing this forces recreation of the resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"workflow_scheme_id": schema.StringAttribute{
				Description: "The ID of the workflow scheme to assign. Updated in place, including projects with issues.",
				Required:    true,
			},
		},
	}
}

func (r *projectWorkflowSchemeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *atlassian.Client, got: %T", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *projectWorkflowSchemeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectWorkflowSchemeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.switchScheme(ctx, plan.ProjectID.ValueString(), plan.WorkflowSchemeID.ValueString(), plan.StatusMappings)
	if err != nil {
		resp.Diagnostics.AddError("Error assigning workflow scheme to project", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectWorkflowSchemeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectWorkflowSchemeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueString()
	apiPath := fmt.Sprintf("/rest/api/3/workflowscheme/project?projectId=%s", atlassian.QueryEscape(projectID))

	// This endpoint returns at most one entry per project — no pagination needed.
	var page atlassian.PageResponse
	status, err := r.client.GetWithStatus(ctx, apiPath, &page)
	if err != nil {
		resp.Diagnostics.AddError("Error reading project workflow scheme", err.Error())
		return
	}
	if status == http.StatusNotFound {
		resp.State.RemoveResource(ctx)
		return
	}

	if len(page.Values) > 1 {
		resp.Diagnostics.AddError("Ambiguous project association", "Jira returned multiple workflow schemes for one project.")
		return
	}
	if len(page.Values) == 1 {
		raw := page.Values[0]
		var entry projectWorkflowSchemeListEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			resp.Diagnostics.AddError("Error parsing project workflow scheme response", err.Error())
			return
		}
		schemeIDStr := fmt.Sprintf("%d", entry.WorkflowScheme.ID)
		state.WorkflowSchemeID = types.StringValue(schemeIDStr)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	// Association not found — remove from state.
	resp.State.RemoveResource(ctx)
}

func (r *projectWorkflowSchemeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan projectWorkflowSchemeResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.switchScheme(ctx, plan.ProjectID.ValueString(), plan.WorkflowSchemeID.ValueString(), plan.StatusMappings); err != nil {
		resp.Diagnostics.AddError("Error switching project workflow scheme", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectWorkflowSchemeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state projectWorkflowSchemeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.DeleteWorkflowSchemeID.IsNull() {
		resp.Diagnostics.AddWarning("Association retained", "No delete_workflow_scheme_id was configured. The project's workflow scheme is unchanged; Terraform only stops managing this association.")
		return
	}
	var project map[string]any
	status, err := r.client.GetWithStatus(ctx, "/rest/api/3/project/"+atlassian.PathEscape(state.ProjectID.ValueString()), &project)
	if err != nil {
		resp.Diagnostics.AddError("Error checking project", err.Error())
		return
	}
	if status == http.StatusNotFound {
		return
	}
	if err := r.switchScheme(ctx, state.ProjectID.ValueString(), state.DeleteWorkflowSchemeID.ValueString(), state.DeleteStatusMappings); err != nil {
		resp.Diagnostics.AddError("Error reassigning workflow scheme on destroy", err.Error())
	}
}

func (r *projectWorkflowSchemeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectID := req.ID

	apiPath := fmt.Sprintf("/rest/api/3/workflowscheme/project?projectId=%s", atlassian.QueryEscape(projectID))

	// This endpoint returns at most one entry per project — no pagination needed.
	var page atlassian.PageResponse
	if err := r.client.Get(ctx, apiPath, &page); err != nil {
		resp.Diagnostics.AddError("Error importing project workflow scheme", err.Error())
		return
	}

	if len(page.Values) == 0 {
		resp.Diagnostics.AddError(
			"Project workflow scheme not found",
			fmt.Sprintf("No workflow scheme association found for project ID %q", projectID),
		)
		return
	}

	var entry projectWorkflowSchemeListEntry
	if err := json.Unmarshal(page.Values[0], &entry); err != nil {
		resp.Diagnostics.AddError("Error parsing project workflow scheme response", err.Error())
		return
	}
	schemeIDStr := fmt.Sprintf("%d", entry.WorkflowScheme.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workflow_scheme_id"), schemeIDStr)...)
}
