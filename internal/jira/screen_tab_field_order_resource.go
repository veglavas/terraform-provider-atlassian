package jira

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &screenTabFieldOrderResource{}
	_ resource.ResourceWithImportState = &screenTabFieldOrderResource{}
)

// NewScreenTabFieldOrderResource returns a new screen tab field order resource.
func NewScreenTabFieldOrderResource() resource.Resource {
	return &screenTabFieldOrderResource{}
}

type screenTabFieldOrderResource struct {
	client *atlassian.Client
}

type screenTabFieldOrderResourceModel struct {
	ID       types.String `tfsdk:"id"`
	ScreenID types.String `tfsdk:"screen_id"`
	TabID    types.String `tfsdk:"tab_id"`
	FieldIDs types.List   `tfsdk:"field_ids"`
}

func (r *screenTabFieldOrderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_screen_tab_field_order"
}

func (r *screenTabFieldOrderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the order of all fields on a Jira Cloud screen tab through documented public APIs. " +
			"It configures Jira and agent-side JSM Create/Edit screens only; native JSM customer-portal request forms and the modern issue-view layout have independent layouts this resource does not control. " +
			"The field_ids list must enumerate the tab's complete membership exactly once, in the desired order from first to last; field membership itself stays owned by atlassian_jira_screen_tab_field resources, which this resource never adds or removes. " +
			"Creation and updates wait up to 30 seconds for field membership to become visible before any move, then verify the resulting order with a separate bounded wait. " +
			"Use exactly one atlassian_jira_screen_tab_field_order resource per tab and depend on every field placement for that tab, so placements exist before ordering and destroy relinquishes ordering before removing the fields. " +
			"Destroying this resource relinquishes order ownership only: no fields are removed or reordered, because a field order is not a deletable Jira object and restoring an unknown previous order would be destructive.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Synthetic ID in the format {screen_id}/{tab_id}.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"screen_id": schema.StringAttribute{
				Description: "The ID of the screen that owns the tab. Changing this replaces this order controller only; the tab's field membership is never modified.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tab_id": schema.StringAttribute{
				Description: "The ID of the screen tab whose fields are ordered. Changing this replaces this order controller only; the tab's field membership is never modified.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"field_ids": schema.ListAttribute{
				Description: "The complete, ordered list of field IDs on the tab, first to last, listing each member exactly once. An empty list is valid only for a tab with no fields. Unknown values during planning (e.g. custom field IDs from other resources) are fine once they resolve at apply time.",
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.UniqueValues(),
					listvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
		},
	}
}

func (r *screenTabFieldOrderResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// screenTabFieldOrderDesiredIDs converts the configured list into a plain
// slice, rejecting values that are still unknown or null at apply time with an
// actionable diagnostic instead of failing inside the HTTP reconciliation.
func screenTabFieldOrderDesiredIDs(ctx context.Context, fieldIDs types.List) ([]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if fieldIDs.IsNull() || fieldIDs.IsUnknown() {
		diags.AddError(
			"Invalid field_ids",
			"field_ids must be a known list of field ID strings at apply time (got null or unknown)",
		)
		return nil, diags
	}

	elements := fieldIDs.Elements()
	desired := make([]string, 0, len(elements))
	seen := make(map[string]bool, len(elements))
	for _, element := range elements {
		s, ok := element.(types.String)
		if !ok || s.IsNull() || s.IsUnknown() {
			diags.AddError(
				"Unresolvable field_ids entry",
				"field_ids contains a null or unknown value that could not be resolved at apply time; every entry must be a known field ID string such as \"summary\" or a custom field ID from atlassian_jira_custom_field",
			)
			return nil, diags
		}
		id := s.ValueString()
		if id == "" || seen[id] {
			diags.AddError("Invalid field_ids entry", "field_ids must contain unique, nonempty field IDs after all values resolve at apply time.")
			return nil, diags
		}
		seen[id] = true
		desired = append(desired, id)
	}

	return desired, diags
}

func (r *screenTabFieldOrderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan screenTabFieldOrderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	desired, diags := screenTabFieldOrderDesiredIDs(ctx, plan.FieldIDs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	screenID := plan.ScreenID.ValueString()
	tabID := plan.TabID.ValueString()

	if err := reconcileScreenTabFieldOrder(ctx, r.client, screenID, tabID, desired); err != nil {
		resp.Diagnostics.AddError("Error ordering screen tab fields", err.Error())
		return
	}

	// The verified remote order now equals the plan; keep the plan values.
	plan.ID = types.StringValue(screenTabFieldOrderID(screenID, tabID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *screenTabFieldOrderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state screenTabFieldOrderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	actual, statusCode, err := getScreenTabFieldIDs(ctx, r.client, state.ScreenID.ValueString(), state.TabID.ValueString())
	if err != nil {
		// Transport errors and malformed remote orders surface as errors; the
		// resource is kept so the failure is visible rather than silently
		// removing state.
		resp.Diagnostics.AddError("Error reading screen tab fields", err.Error())
		return
	}
	if statusCode == http.StatusNotFound {
		// Screen or tab deleted out-of-band.
		resp.State.RemoveResource(ctx)
		return
	}

	// Persist the actual remote order so out-of-band reordering (or membership
	// changes) surface as drift on the next plan. Membership mismatches are
	// corrected by their placement owners; a future apply enforces the
	// exact-membership precondition before writing.
	fieldIDs, diags := types.ListValueFrom(ctx, types.StringType, actual)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.ID = types.StringValue(screenTabFieldOrderID(state.ScreenID.ValueString(), state.TabID.ValueString()))
	state.FieldIDs = fieldIDs
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *screenTabFieldOrderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan screenTabFieldOrderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	desired, diags := screenTabFieldOrderDesiredIDs(ctx, plan.FieldIDs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := reconcileScreenTabFieldOrder(ctx, r.client, plan.ScreenID.ValueString(), plan.TabID.ValueString(), desired); err != nil {
		resp.Diagnostics.AddError("Error ordering screen tab fields", err.Error())
		return
	}

	// screen_id and tab_id are RequiresReplace; only the order changes here.
	plan.ID = types.StringValue(screenTabFieldOrderID(plan.ScreenID.ValueString(), plan.TabID.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete intentionally makes no API calls. A field order is not a deletable
// Jira object: removing the resource relinquishes order ownership and leaves
// the tab's field membership and its current remote order untouched. Deleting
// fields or guessing a "previous" order would be destructive and is out of
// scope for this resource.
func (r *screenTabFieldOrderResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *screenTabFieldOrderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	screenID, tabID, err := parseScreenTabFieldOrderID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("screen_id"), screenID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tab_id"), tabID)...)
	// field_ids is populated from the remote order by the subsequent Read.
}
