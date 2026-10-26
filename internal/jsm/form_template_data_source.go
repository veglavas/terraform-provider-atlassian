package jsm

import (
	"context"
	"fmt"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

type formTemplateDataSource struct{ client *atlassian.Client }

var _ datasource.DataSource = &formTemplateDataSource{}

func NewFormTemplateDataSource() datasource.DataSource { return &formTemplateDataSource{} }
func (d *formTemplateDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jsm_form_template"
}
func (d *formTemplateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Reads an Atlassian Forms project template by project and UUID, using Basic authentication and automatic cloud ID discovery. Returns the complete design as reusable request JSON (default-answer users become account IDs), plus publishing settings.", Attributes: map[string]schema.Attribute{
		"id":         schema.StringAttribute{Required: true, Description: "The form template UUID.", Validators: []validator.String{stringvalidator.RegexMatches(formIDPattern, "must be a form UUID")}},
		"project_id": schema.StringAttribute{Required: true, Description: "Jira project ID or key (not service desk ID).", Validators: []validator.String{stringvalidator.RegexMatches(formProjectPattern, "must be a Jira project ID or key")}},
		"design":     schema.StringAttribute{Computed: true, CustomType: formJSONType{}, Description: formDesignDescription},
		"publish":    schema.StringAttribute{Computed: true, CustomType: formJSONType{}, Description: formPublishDescription},
	}}
}
func (d *formTemplateDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func (d *formTemplateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model formTemplateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := readFormTemplate(ctx, d.client, &model, nil)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read form template", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Form template not found", "No accessible form template was found for this project and UUID.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
