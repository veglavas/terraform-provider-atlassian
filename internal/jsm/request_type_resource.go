package jsm

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_         resource.Resource                = &requestTypeResource{}
	_         resource.ResourceWithImportState = &requestTypeResource{}
	numericID                                  = regexp.MustCompile(`^[0-9]+$`)
)

func NewRequestTypeResource() resource.Resource { return &requestTypeResource{} }

type requestTypeResource struct{ client *atlassian.Client }

type requestTypeModel struct {
	ID            types.String `tfsdk:"id"`
	ServiceDeskID types.String `tfsdk:"service_desk_id"`
	IssueTypeID   types.String `tfsdk:"issue_type_id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	HelpText      types.String `tfsdk:"help_text"`
	PortalID      types.String `tfsdk:"portal_id"`
	GroupIDs      types.Set    `tfsdk:"group_ids"`
}

type requestTypeAPI struct {
	ID            string   `json:"id"`
	ServiceDeskID string   `json:"serviceDeskId"`
	IssueTypeID   string   `json:"issueTypeId"`
	Name          string   `json:"name"`
	Description   *string  `json:"description"`
	HelpText      *string  `json:"helpText"`
	PortalID      string   `json:"portalId"`
	GroupIDs      []string `json:"groupIds"`
}

type requestTypeWrite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	HelpText    string `json:"helpText"`
	IssueTypeID string `json:"issueTypeId"`
}

func requestTypesPath(serviceDeskID string) string {
	return "/rest/servicedeskapi/servicedesk/" + atlassian.PathEscape(serviceDeskID) + "/requesttype"
}
func requestTypePath(serviceDeskID, id string) string {
	return requestTypesPath(serviceDeskID) + "/" + atlassian.PathEscape(id)
}

func (r *requestTypeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jsm_request_type"
}
func (r *requestTypeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	idValidators := []validator.String{stringvalidator.RegexMatches(numericID, "must be a numeric ID")}
	immutable := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Manages a Jira Service Management request type in a company-managed (classic) service project. Requires service desk administrator permissions. Creation/deletion use documented experimental APIs. All configurable changes require replacement: no public update endpoint exists. Deletion removes this request type from existing customer requests. Use prevent_destroy for production request types. New request types are hidden from the portal until grouped manually; portal groups, forms, icons, and status mappings cannot be configured here.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, Description: "The request type ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"service_desk_id": schema.StringAttribute{Required: true, Description: "The numeric service desk ID, not the Jira project ID or key. Changing this replaces the request type.", Validators: idValidators, PlanModifiers: immutable},
			"issue_type_id":   schema.StringAttribute{Required: true, Description: "The existing parent issue type ID to associate with this request type. Changing this replaces the request type.", Validators: idValidators, PlanModifiers: immutable},
			"name":            schema.StringAttribute{Required: true, Description: "The request type name. Changing this replaces the request type.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}, PlanModifiers: immutable},
			"description":     schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "The request type description. Changing this replaces the request type.", PlanModifiers: immutable},
			"help_text":       schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "The request type help text. Changing this replaces the request type.", PlanModifiers: immutable},
			"portal_id":       schema.StringAttribute{Computed: true, Description: "The customer portal ID."},
			"group_ids":       schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Observed portal group IDs. Read-only; configure grouping in Jira. Empty means the request type is hidden from portal browsing."},
		},
	}
}
func (r *requestTypeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// An individual read avoids hidden-type filtering and pagination ambiguity.
func readRequestType(ctx context.Context, client *atlassian.Client, deskID, id string) (*requestTypeAPI, error) {
	var result requestTypeAPI
	status, err := client.GetWithStatus(ctx, requestTypePath(deskID, id), &result)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if result.ID != id || result.ServiceDeskID != deskID || result.IssueTypeID == "" || result.Name == "" || result.PortalID == "" {
		return nil, fmt.Errorf("incomplete or mismatched request type response for %s/%s; preserving state", deskID, id)
	}
	return &result, nil
}

func setRequestTypeModel(ctx context.Context, model *requestTypeModel, result *requestTypeAPI) error {
	// Jira can omit optional empty metadata. Do not interpret an omitted/null
	// field as clearing previously observed nonempty metadata: that could
	// produce a destructive replacement from an incomplete response.
	if (result.Description == nil && model.Description.ValueString() != "") || (result.HelpText == nil && model.HelpText.ValueString() != "") {
		return fmt.Errorf("request type response omitted previously nonempty description or help text; preserving state; verify Jira metadata before retrying")
	}
	model.ID = types.StringValue(result.ID)
	model.ServiceDeskID = types.StringValue(result.ServiceDeskID)
	model.IssueTypeID = types.StringValue(result.IssueTypeID)
	model.Name = types.StringValue(result.Name)
	model.Description = types.StringValue("")
	if result.Description != nil {
		model.Description = types.StringValue(*result.Description)
	}
	model.HelpText = types.StringValue("")
	if result.HelpText != nil {
		model.HelpText = types.StringValue(*result.HelpText)
	}
	model.PortalID = types.StringValue(result.PortalID)
	groups := result.GroupIDs
	if groups == nil {
		groups = []string{}
	}
	groupSet, diags := types.SetValueFrom(ctx, types.StringType, groups)
	if diags.HasError() {
		return fmt.Errorf("converting request type groups: %v", diags)
	}
	model.GroupIDs = groupSet
	return nil
}

func (r *requestTypeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan requestTypeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := requestTypeWrite{Name: plan.Name.ValueString(), Description: plan.Description.ValueString(), HelpText: plan.HelpText.ValueString(), IssueTypeID: plan.IssueTypeID.ValueString()}
	var created requestTypeAPI
	if err := r.client.Post(ctx, requestTypesPath(plan.ServiceDeskID.ValueString()), body, &created, atlassian.WithExperimentalAPI()); err != nil {
		resp.Diagnostics.AddError("Error creating request type", err.Error())
		return
	}
	if !numericID.MatchString(created.ID) {
		resp.Diagnostics.AddError("Missing request type ID", "Jira returned no valid request type ID. The request type may have been created; inspect Jira before retrying to avoid duplicates.")
		return
	}
	plan.ID = types.StringValue(created.ID)
	plan.PortalID = types.StringNull()
	plan.GroupIDs = types.SetNull(types.StringType)
	// Retain the ID if visibility fails. Terraform may taint this state: inspect
	// and recover deliberately rather than blindly reapplying a replacement.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		result, err := readRequestType(waitCtx, r.client, plan.ServiceDeskID.ValueString(), created.ID)
		if err != nil {
			resp.Diagnostics.AddError("Error waiting for request type", err.Error())
			return
		}
		if result != nil {
			// POST can be partial; preserve configured values, populating only computed
			// fields from GET. A subsequent refresh detects any server-side drift.
			plan.PortalID = types.StringValue(result.PortalID)
			groups := result.GroupIDs
			if groups == nil {
				groups = []string{}
			}
			groupSet, diags := types.SetValueFrom(ctx, types.StringType, groups)
			resp.Diagnostics.Append(diags...)
			plan.GroupIDs = groupSet
			if !resp.Diagnostics.HasError() {
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			}
			return
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			resp.Diagnostics.AddError("Error waiting for request type", waitCtx.Err().Error())
			return
		case <-timer.C:
		}
	}
}
func (r *requestTypeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state requestTypeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := readRequestType(ctx, r.client, state.ServiceDeskID.ValueString(), state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading request type", err.Error())
		return
	}
	if result == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	if err := setRequestTypeModel(ctx, &state, result); err != nil {
		resp.Diagnostics.AddError("Error reading request type", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *requestTypeResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Request type update is not supported", "The public JSM API has no request type update endpoint. All configurable attributes require replacement. Protect production request types with prevent_destroy.")
}
func (r *requestTypeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state requestTypeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.DeleteWithStatus(ctx, requestTypePath(state.ServiceDeskID.ValueString(), state.ID.ValueString()), atlassian.WithExperimentalAPI()); err != nil {
		resp.Diagnostics.AddError("Error deleting request type", err.Error())
	}
}
func (r *requestTypeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || !numericID.MatchString(parts[0]) || !numericID.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid import ID", "Expected {service_desk_id}/{request_type_id}, both numeric IDs (not the Jira project ID or key).")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_desk_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
