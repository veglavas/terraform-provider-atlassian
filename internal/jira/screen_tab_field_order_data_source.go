package jira

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ datasource.DataSource = &screenTabFieldOrderDataSource{}

// NewScreenTabFieldOrderDataSource returns a new screen tab field order data source.
func NewScreenTabFieldOrderDataSource() datasource.DataSource {
	return &screenTabFieldOrderDataSource{}
}

type screenTabFieldOrderDataSource struct {
	client *atlassian.Client
}

type screenTabFieldOrderDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	ScreenID types.String `tfsdk:"screen_id"`
	TabID    types.String `tfsdk:"tab_id"`
	FieldIDs types.List   `tfsdk:"field_ids"`
}

func (d *screenTabFieldOrderDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_screen_tab_field_order"
}

func (d *screenTabFieldOrderDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Use this data source to read the current field order of a Jira Cloud screen tab. " +
			"It reads Jira and agent-side JSM Create/Edit screens only; it does not describe native JSM customer-portal request form layouts.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Synthetic ID in the format {screen_id}/{tab_id}.",
				Computed:    true,
			},
			"screen_id": schema.StringAttribute{
				Description: "The ID of the screen that owns the tab.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"tab_id": schema.StringAttribute{
				Description: "The ID of the screen tab.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"field_ids": schema.ListAttribute{
				Description: "The field IDs on the tab, in the remote order from first to last.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *screenTabFieldOrderDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *atlassian.Client, got: %T", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *screenTabFieldOrderDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config screenTabFieldOrderDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	screenID := config.ScreenID.ValueString()
	tabID := config.TabID.ValueString()

	actual, statusCode, err := getScreenTabFieldIDs(ctx, d.client, screenID, tabID)
	if err != nil {
		// Transport errors and malformed remote orders are diagnostics for a
		// data source; there is no resource state to remove.
		resp.Diagnostics.AddError("Error reading screen tab fields", err.Error())
		return
	}
	if statusCode == http.StatusNotFound {
		// A missing screen or tab is a data source error, not a removal.
		resp.Diagnostics.AddError(
			"Screen or screen tab not found",
			fmt.Sprintf("GET /rest/api/3/screens/%s/tabs/%s/fields returned HTTP 404: no screen %s or tab %s exists",
				screenID, tabID, screenID, tabID),
		)
		return
	}

	fieldIDs, diags := types.ListValueFrom(ctx, types.StringType, actual)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	config.ID = types.StringValue(screenTabFieldOrderID(screenID, tabID))
	config.FieldIDs = fieldIDs
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
