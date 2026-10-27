package jira

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ datasource.DataSource = &projectFieldSchemeDataSource{}

// NewProjectFieldSchemeDataSource discovers a project's field association scheme.
func NewProjectFieldSchemeDataSource() datasource.DataSource { return &projectFieldSchemeDataSource{} }

type projectFieldSchemeDataSource struct{ client *atlassian.Client }
type projectFieldSchemeDataSourceModel struct {
	ProjectID   types.String `tfsdk:"project_id"`
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func (d *projectFieldSchemeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_project_field_scheme"
}
func (d *projectFieldSchemeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Discovers the existing Jira field association scheme for a project using the experimental Field schemes API. Read-only; does not assign or create a scheme. A scheme may be shared by multiple projects. Requires Administer Jira permissions.",
		Attributes: map[string]schema.Attribute{
			"project_id":  schema.StringAttribute{Required: true, Description: "Numeric Jira project ID, not its key.", Validators: []validator.String{fieldSchemeIDValidator{}}},
			"id":          schema.StringAttribute{Computed: true, Description: "Field association scheme ID."},
			"name":        schema.StringAttribute{Computed: true, Description: "Field association scheme name."},
			"description": schema.StringAttribute{Computed: true, Description: "Field association scheme description."},
		},
	}
}
func (d *projectFieldSchemeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *atlassian.Client, got: %T", req.ProviderData))
		return
	}
	d.client = client
}
func (d *projectFieldSchemeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config projectFieldSchemeDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	scheme, err := readProjectFieldScheme(ctx, d.client, config.ProjectID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading project field scheme", err.Error())
		return
	}
	config.ID = types.StringValue(scheme.ID)
	config.Name = types.StringValue(scheme.Name)
	config.Description = types.StringValue(scheme.Description)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
