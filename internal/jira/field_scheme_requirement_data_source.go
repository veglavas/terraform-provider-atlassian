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

var _ datasource.DataSource = &fieldSchemeRequirementDataSource{}

// NewFieldSchemeRequirementDataSource reads an effective field requirement.
func NewFieldSchemeRequirementDataSource() datasource.DataSource {
	return &fieldSchemeRequirementDataSource{}
}

type fieldSchemeRequirementDataSource struct{ client *atlassian.Client }

func (d *fieldSchemeRequirementDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme_requirement"
}
func (d *fieldSchemeRequirementDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads the effective required flag for an existing field and issue type in a Jira field association scheme using the experimental Field schemes API. Falls back to the scheme default when no required override is returned. Does not describe native JSM request-form visibility.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Computed: true, Description: "Composite ID: SCHEME_ID:FIELD_ID:ISSUE_TYPE_ID."},
			"scheme_id":     schema.StringAttribute{Required: true, Description: "Existing field association scheme ID.", Validators: []validator.String{fieldSchemeIDValidator{}}},
			"field_id":      schema.StringAttribute{Required: true, Description: "Existing associated Jira field ID.", Validators: []validator.String{stringvalidator.RegexMatches(fieldSchemeFieldIDPattern, "must be a Jira field ID containing only letters, digits, and underscores")}},
			"issue_type_id": schema.StringAttribute{Required: true, Description: "Issue type (work type) ID.", Validators: []validator.String{fieldSchemeIDValidator{}}},
			"is_required":   schema.BoolAttribute{Computed: true, Description: "Effective required flag for the field and issue type."},
		},
	}
}
func (d *fieldSchemeRequirementDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func (d *fieldSchemeRequirementDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config fieldSchemeRequirementModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	required, found, err := readFieldRequirement(ctx, d.client, config.key())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field requirement", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Field requirement not found", "The scheme or field association does not exist, or the field is unavailable to this issue type.")
		return
	}
	config.ID = types.StringValue(config.key().id())
	config.IsRequired = types.BoolValue(required)
	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
