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

var _ datasource.DataSource = &customFieldOptionDataSource{}

func NewCustomFieldOptionDataSource() datasource.DataSource { return &customFieldOptionDataSource{} }

type customFieldOptionDataSource struct{ client *atlassian.Client }

func (d *customFieldOptionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_custom_field_option"
}

func (d *customFieldOptionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	required := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, Description: description, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}}
	}
	resp.Schema = schema.Schema{Description: "Looks up a Jira custom field option by field, context and option ID, including cascading children. Requires Administer Jira.", Attributes: map[string]schema.Attribute{
		"id":               required("The option ID."),
		"field_id":         required("The custom field ID."),
		"context_id":       required("The context ID."),
		"value":            schema.StringAttribute{Computed: true, Description: "The option's display value."},
		"disabled":         schema.BoolAttribute{Computed: true, Description: "Whether the option is disabled."},
		"parent_option_id": schema.StringAttribute{Computed: true, Description: "Parent option ID for cascading children; null for top-level options."},
	}}
}

func (d *customFieldOptionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *customFieldOptionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config customFieldOptionModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	option, err := findFieldOption(ctx, d.client, config.FieldID.ValueString(), config.ContextID.ValueString(), config.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading custom field option", err.Error())
		return
	}
	if option == nil {
		resp.Diagnostics.AddError("Custom field option not found", "No option matched the specified field, context and option ID.")
		return
	}
	config.Value, config.Disabled = types.StringValue(option.Value), types.BoolValue(option.Disabled)
	config.ParentOptionID = types.StringNull()
	if option.ParentID != "" {
		config.ParentOptionID = types.StringValue(option.ParentID)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
