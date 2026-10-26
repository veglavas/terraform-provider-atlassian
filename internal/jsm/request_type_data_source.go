package jsm

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ datasource.DataSource = &requestTypeDataSource{}

func NewRequestTypeDataSource() datasource.DataSource { return &requestTypeDataSource{} }

type requestTypeDataSource struct{ client *atlassian.Client }

func (d *requestTypeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jsm_request_type"
}
func (d *requestTypeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	ids := []validator.String{stringvalidator.RegexMatches(numericID, "must be a numeric ID")}
	resp.Schema = schema.Schema{Description: "Reads a Jira Service Management request type by service desk ID and request type ID, including types hidden from portal browsing. Requires access to the service desk.", Attributes: map[string]schema.Attribute{
		"id":              schema.StringAttribute{Required: true, Description: "The request type ID.", Validators: ids},
		"service_desk_id": schema.StringAttribute{Required: true, Description: "The numeric service desk ID, not the Jira project ID or key.", Validators: ids},
		"issue_type_id":   schema.StringAttribute{Computed: true, Description: "The associated issue type ID."},
		"name":            schema.StringAttribute{Computed: true, Description: "The request type name."},
		"description":     schema.StringAttribute{Computed: true, Description: "The request type description."},
		"help_text":       schema.StringAttribute{Computed: true, Description: "The request type help text."},
		"portal_id":       schema.StringAttribute{Computed: true, Description: "The customer portal ID."},
		"group_ids":       schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Observed portal group IDs. Empty means hidden from portal browsing."},
	}}
}
func (d *requestTypeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	d.client = client
}
func (d *requestTypeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config requestTypeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := readRequestType(ctx, d.client, config.ServiceDeskID.ValueString(), config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading request type", err.Error())
		return
	}
	if result == nil {
		resp.Diagnostics.AddError("Request type not found", "No request type exists with the specified service desk ID and request type ID.")
		return
	}
	if err := setRequestTypeModel(ctx, &config, result); err != nil {
		resp.Diagnostics.AddError("Error reading request type", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
