package jira

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ datasource.DataSource = &customFieldContextDataSource{}

func NewCustomFieldContextDataSource() datasource.DataSource { return &customFieldContextDataSource{} }

type customFieldContextDataSource struct{ client *atlassian.Client }

func (d *customFieldContextDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_custom_field_context"
}

func (d *customFieldContextDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Looks up a Jira custom field context by ID or exact name. Specify exactly one of id and name. Ambiguous names fail rather than selecting an arbitrary context. Requires Administer Jira.", Attributes: map[string]schema.Attribute{
		"field_id":       schema.StringAttribute{Required: true, Description: "The custom field ID.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"id":             schema.StringAttribute{Optional: true, Computed: true, Description: "The context ID. Specify this or name.", Validators: []validator.String{stringvalidator.ExactlyOneOf(path.MatchRoot("name")), stringvalidator.LengthAtLeast(1)}},
		"name":           schema.StringAttribute{Optional: true, Computed: true, Description: "The exact context name. Specify this or id.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
		"description":    schema.StringAttribute{Computed: true, Description: "The context description."},
		"project_ids":    schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Project IDs; empty for a global context."},
		"issue_type_ids": schema.SetAttribute{Computed: true, ElementType: types.StringType, Description: "Issue type IDs; empty for all issue types."},
	}}
}

func (d *customFieldContextDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *customFieldContextDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config customFieldContextModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := findFieldContext(ctx, d.client, config.FieldID.ValueString(), config.ID.ValueString(), config.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field context", err.Error())
		return
	}
	if c == nil {
		resp.Diagnostics.AddError("Custom field context not found", "No context matched the specified field and ID/name.")
		return
	}
	projects, issueTypes, missing, err := readFieldContextScope(ctx, d.client, config.FieldID.ValueString(), *c)
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field context scope", err.Error())
		return
	}
	if missing {
		resp.Diagnostics.AddError("Custom field context not found", "The context disappeared while reading its scope.")
		return
	}
	config.ID, config.Name, config.Description = types.StringValue(c.ID), types.StringValue(c.Name), types.StringValue(c.Description)
	projectsSet, diags := types.SetValueFrom(ctx, types.StringType, projects)
	resp.Diagnostics.Append(diags...)
	typesSet, diags := types.SetValueFrom(ctx, types.StringType, issueTypes)
	resp.Diagnostics.Append(diags...)
	config.ProjectIDs, config.IssueTypeIDs = projectsSet, typesSet
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
	}
}
