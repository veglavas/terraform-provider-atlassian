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

func NewFieldSchemeDataSource() datasource.DataSource { return &fieldSchemeDataSource{} }

type fieldSchemeDataSource struct{ client *atlassian.Client }

func (d *fieldSchemeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme"
}
func (d *fieldSchemeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Reads a Jira field scheme by ID using the experimental Field schemes API. Requires Administer Jira.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Required: true, Description: "Field scheme ID.", Validators: []validator.String{fieldSchemeIDValidator{}}},
		"name":        schema.StringAttribute{Computed: true, Description: "Scheme name."},
		"description": schema.StringAttribute{Computed: true, Description: "Scheme description."},
	}}
}
func (d *fieldSchemeDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	d.client = c
}
func (d *fieldSchemeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config fieldSchemeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := readManagedFieldScheme(ctx, d.client, config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field scheme", err.Error())
		return
	}
	if s == nil {
		resp.Diagnostics.AddError("Field scheme not found", "No field scheme matches the configured ID.")
		return
	}
	config.Name = types.StringValue(*s.Name)
	config.Description = types.StringValue("")
	if s.Description != nil {
		config.Description = types.StringValue(*s.Description)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
