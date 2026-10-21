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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_ resource.Resource                = &workflowResource{}
	_ resource.ResourceWithImportState = &workflowResource{}
)

// NewWorkflowResource returns a new workflow resource.
func NewWorkflowResource() resource.Resource {
	return &workflowResource{}
}

type workflowResource struct {
	client *atlassian.Client
}

type workflowResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	Statuses       types.List   `tfsdk:"statuses"`
	Transitions    types.Map    `tfsdk:"transitions"`
	StatusMappings types.Map    `tfsdk:"status_mappings"`
}

type workflowAPIItem struct {
	Definition workflowDefinition `json:"-"`
	ID         struct {
		Name     string `json:"name"`
		EntityID string `json:"entityId"`
	} `json:"id"`
	Description string `json:"description"`
	Statuses    []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"statuses"`
}

func (r *workflowResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_workflow"
}

func (r *workflowResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Jira Cloud workflow. Description, statuses, and configured transitions update in place; unmanaged transitions and rules are preserved.",
		Attributes: map[string]schema.Attribute{
			"transitions":     workflowTransitionsSchema(),
			"status_mappings": schema.MapAttribute{Optional: true, ElementType: types.StringType, Description: "Explicit old status ID to new status ID mappings for removed workflow statuses. Used as default migration mappings for all affected projects and issue types."},
			"id": schema.StringAttribute{
				Description: "The entity ID (UUID) of the workflow.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The name of the workflow. Changing this forces recreation of the resource.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Description: "The description of the workflow. Updated in place.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"statuses": schema.ListAttribute{
				Description: "Ordered list of existing Jira status IDs. Updated in place. Removing statuses requires explicit status_mappings destinations and compatible transitions.",
				Required:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *workflowResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workflowResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workflowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Extract status IDs from the plan list
	var statusIDs []string
	resp.Diagnostics.Append(plan.Statuses.ElementsAs(ctx, &statusIDs, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if len(statusIDs) == 0 {
		resp.Diagnostics.AddError("Invalid workflow configuration", "At least one status is required")
		return
	}

	body, err := r.workflowPayload(ctx, plan.Name.ValueString(), plan.Description.ValueString(), statusIDs, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error preparing workflow", err.Error())
		return
	}
	if err := applyWorkflowTransitions(ctx, body, plan.Transitions, types.MapNull(types.ObjectType{AttrTypes: workflowTransitionTypes})); err != nil {
		resp.Diagnostics.AddError("Invalid transitions", err.Error())
		return
	}
	result, err := r.mutateWorkflow(ctx, "create", body)
	if err != nil {
		resp.Diagnostics.AddError("Error creating workflow", err.Error())
		return
	}

	// Only take the ID from the response; preserve all other plan values.
	if len(result.Workflows) != 1 || definitionString(result.Workflows[0], "id") == "" {
		resp.Diagnostics.AddError("Invalid create response", "Jira did not return a workflow ID. Check Jira before retrying to avoid duplicate creation.")
		return
	}
	plan.ID = types.StringValue(definitionString(result.Workflows[0], "id"))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workflowResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workflowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found, err := r.findWorkflowByID(ctx, state.ID.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading workflow", err.Error())
		return
	}

	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(found.ID.EntityID)
	state.Name = types.StringValue(found.ID.Name)
	state.Description = types.StringValue(found.Description)

	// Company-managed workflow references are existing Jira status IDs.
	statusRefs := make([]string, len(found.Statuses))
	for i, s := range found.Statuses {
		statusRefs[i] = s.ID
	}

	statusRefs = stableWorkflowIDs(ctx, state.Statuses, statusRefs)
	statusList, diags := types.ListValueFrom(ctx, types.StringType, statusRefs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Statuses = statusList
	state.Transitions, err = refreshWorkflowTransitions(ctx, state.Transitions, found.Definition)
	if err != nil {
		resp.Diagnostics.AddError("Error refreshing transitions", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// findWorkflowByID reads the complete definition by stable ID, including imports.
func (r *workflowResource) findWorkflowByID(ctx context.Context, entityID, _ string) (*workflowAPIItem, error) {
	// Resolve by immutable ID, never by the stale name in state.
	definition, err := r.definition(ctx, entityID)
	if err != nil || definition == nil {
		return nil, err
	}
	item := &workflowAPIItem{Definition: definition}
	item.ID.EntityID = definitionString(definition, "id")
	item.ID.Name = definitionString(definition, "name")
	item.Description = definitionString(definition, "description")
	var statuses []struct {
		Reference string `json:"statusReference"`
	}
	if err := json.Unmarshal(definition["statuses"], &statuses); err != nil {
		return nil, fmt.Errorf("decoding workflow statuses: %w", err)
	}
	for _, status := range statuses {
		item.Statuses = append(item.Statuses, struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}{ID: status.Reference})
	}
	return item, nil
}

func (r *workflowResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workflowResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.definition(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading workflow before update", err.Error())
		return
	}
	if current == nil {
		resp.Diagnostics.AddError("Workflow not found", "The workflow disappeared before update. Refresh state before retrying.")
		return
	}
	var ids []string
	resp.Diagnostics.Append(plan.Statuses.ElementsAs(ctx, &ids, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := requireRemovedWorkflowMappings(ctx, current, ids, plan.StatusMappings); err != nil {
		resp.Diagnostics.AddError("Workflow migration mappings required", err.Error())
		return
	}
	body, err := r.workflowPayload(ctx, plan.Name.ValueString(), plan.Description.ValueString(), ids, current)
	if err == nil {
		err = applyWorkflowStatusMappings(ctx, body, plan.StatusMappings)
	}
	if err == nil {
		err = applyWorkflowTransitions(ctx, body, plan.Transitions, state.Transitions)
	}
	if err == nil {
		_, err = r.mutateWorkflow(ctx, "update", body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating workflow", err.Error())
		return
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workflowResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workflowResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiPath := fmt.Sprintf("/rest/api/3/workflow/%s", atlassian.PathEscape(state.ID.ValueString()))
	statusCode, err := r.client.DeleteWithStatus(ctx, apiPath)
	if err != nil {
		resp.Diagnostics.AddError("Error deleting workflow", err.Error())
		return
	}

	// 404 means the workflow was already deleted out-of-band; treat as success.
	if statusCode == http.StatusNotFound {
		return
	}
}

func (r *workflowResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
