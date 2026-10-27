package jira

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

func NewFieldSchemeFieldDataSource() datasource.DataSource { return &fieldSchemeFieldDataSource{} }

type fieldSchemeFieldDataSource struct{ client *atlassian.Client }

func (d *fieldSchemeFieldDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme_field"
}
func (d *fieldSchemeFieldDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Reads a field association and its issue-type restrictions from a Jira field scheme using the experimental API. Requires Administer Jira.", Attributes: map[string]schema.Attribute{
		"id":             schema.StringAttribute{Computed: true, Description: "SCHEME_ID:FIELD_ID."},
		"scheme_id":      schema.StringAttribute{Required: true, Description: "Field scheme ID.", Validators: []validator.String{fieldSchemeIDValidator{}}},
		"field_id":       schema.StringAttribute{Required: true, Description: "Jira field ID.", Validators: []validator.String{stringvalidator.RegexMatches(fieldSchemeFieldIDPattern, "must be a Jira field ID")}},
		"issue_type_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Restricted issue types. Empty means all issue types."},
	}}
}
func (d *fieldSchemeFieldDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func (d *fieldSchemeFieldDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config fieldSchemeFieldModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	f, err := readManagedFieldAssociation(ctx, d.client, config.SchemeID.ValueString(), config.FieldID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field association", err.Error())
		return
	}
	if f == nil {
		resp.Diagnostics.AddError("Field association not found", "The field is not associated with this scheme.")
		return
	}
	config.ID = types.StringValue(config.SchemeID.ValueString() + ":" + config.FieldID.ValueString())
	config.IssueTypeIDs, _ = types.SetValueFrom(ctx, types.StringType, nonNilStrings(f.RestrictedToWorkTypes))
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
