package jsm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var (
	_                  resource.ResourceWithImportState = &formTemplateResource{}
	formProjectPattern                                  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
	formIDPattern                                       = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const formDesignDescription = "Normalized JSON FormDesignRequest: conditions, layout, questions, sections and settings. Link compatible existing Jira fields with questions.<id>.jiraField. Layout is opaque ADF; supply a known valid design. Object order and whitespace are insignificant; array order is preserved. Optional API defaults absent from configuration are ignored on refresh."
const formPublishDescription = "Normalized JSON FormPublishing. Set portal.portalRequestTypeIds to numeric request type IDs, plus portal.submitOnCreate and portal.validateOnCreate. Omit to retain API settings; use explicit empty ID arrays to unpublish. This does not manage native request-type fields or remove Summary."

type formTemplateResource struct{ client *atlassian.Client }
type formTemplateModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	Design    formJSON     `tfsdk:"design"`
	Publish   formJSON     `tfsdk:"publish"`
}

func NewFormTemplateResource() resource.Resource { return &formTemplateResource{} }
func (r *formTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jsm_form_template"
}
func (r *formTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an Atlassian Forms project template using the public Forms API and the provider's Basic authentication. The cloud ID is discovered automatically. Requires the documented Jira administration permission for writes. See the Forms specification for layout and field-linking limitations. Creation is never automatically retried; reconcile an ambiguous failure before applying again.",
		Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, Description: "The form template UUID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id": schema.StringAttribute{Required: true, Description: "Jira project ID or key (not service desk ID). Prefer the stable project ID. Changing this replaces the template.", Validators: []validator.String{stringvalidator.RegexMatches(formProjectPattern, "must be a Jira project ID or key")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"design":     schema.StringAttribute{Required: true, CustomType: formJSONType{}, Description: formDesignDescription, Validators: []validator.String{formJSONValidator{"FormDesignRequest"}}},
			"publish":    schema.StringAttribute{Optional: true, Computed: true, CustomType: formJSONType{}, Description: formPublishDescription, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Validators: []validator.String{formJSONValidator{"FormPublishing"}}},
		},
	}
}
func (r *formTemplateResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}
func formTemplatePath(project, id string) string {
	p := "/project/" + atlassian.PathEscape(project) + "/form"
	if id != "" {
		p += "/" + atlassian.PathEscape(id)
	}
	return p
}
func formTemplateBody(model formTemplateModel) map[string]json.RawMessage {
	body := map[string]json.RawMessage{"design": json.RawMessage(model.Design.ValueString())}
	if !model.Publish.IsNull() && !model.Publish.IsUnknown() {
		body["publish"] = json.RawMessage(model.Publish.ValueString())
	}
	return body
}
func (r *formTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model formTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.client.Forms(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to initialize Forms", err.Error())
		return
	}
	var result struct {
		ID string `json:"id"`
	}
	err = client.Post(ctx, formTemplatePath(model.ProjectID.ValueString(), ""), formTemplateBody(model), &result)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create form template", err.Error()+". POST was not retried. The template may have been created; reconcile the project form index and import its UUID before retrying.")
		return
	}
	if !formIDPattern.MatchString(result.ID) {
		resp.Diagnostics.AddError("Invalid create response", "Forms did not return a template UUID. Creation may have succeeded; reconcile the project form index and import before retrying.")
		return
	}
	// FormIndexEntry is partial. Keep every plan value; do not replace it with
	// absent response fields or perform a premature GET that could return 404.
	model.ID = types.StringValue(result.ID)
	if resp.Private != nil {
		resp.Diagnostics.Append(rememberFormConfiguration(ctx, req.Config, model, resp.Private)...)
	}
	unconfiguredPublishing := model.Publish.IsNull() || model.Publish.IsUnknown()
	if unconfiguredPublishing {
		model.Publish = formJSONValue(map[string]any{})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	observed := model
	if err := waitForFormTemplate(ctx, r.client, &observed); err != nil {
		resp.Diagnostics.AddError("Created form template is not yet readable", err.Error()+". The UUID was saved in state, but Terraform may mark it tainted. Verify the template and reconcile state before applying again; do not blindly recreate it.")
		return
	}
	// Computed publication must be populated before downstream expressions run,
	// not left as a placeholder until the next refresh. Preserve configured values.
	if unconfiguredPublishing {
		model.Publish = observed.Publish
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *formTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model formTemplateModel
	var config *formTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if req.Private != nil {
		raw, diags := req.Private.GetKey(ctx, "form_configuration")
		resp.Diagnostics.Append(diags...)
		if len(raw) > 0 {
			var values map[string]json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				resp.Diagnostics.AddError("Invalid private state", "Unable to decode Forms configuration metadata.")
				return
			}
			config = &formTemplateModel{}
			if value, ok := values["design"]; ok {
				config.Design = formJSON{types.StringValue(string(value))}
			}
			if value, ok := values["publish"]; ok {
				config.Publish = formJSON{types.StringValue(string(value))}
			}
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := readFormTemplate(ctx, r.client, &model, config)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read form template", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func readFormTemplate(ctx context.Context, client *atlassian.Client, model *formTemplateModel, config *formTemplateModel) (bool, error) {
	scoped, err := client.Forms(ctx)
	if err != nil {
		return false, err
	}
	var raw json.RawMessage
	status, err := scoped.GetWithStatus(ctx, formTemplatePath(model.ProjectID.ValueString(), model.ID.ValueString()), &raw)
	if err != nil {
		return false, err
	}
	if status == http.StatusNotFound {
		return false, nil
	}
	decoded, err := decodeFormJSON(string(raw))
	if err != nil {
		return false, err
	}
	obj, ok := decoded.(map[string]any)
	if !ok || obj["id"] != model.ID.ValueString() {
		return false, fmt.Errorf("incomplete or mismatched form template response; preserving state")
	}
	design, ok := obj["design"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("form template response is missing design; preserving state")
	}
	if err := validateFormSchema(design, formSchemas["FormDesign"], "design", true); err != nil {
		return false, err
	}
	requestFormDesign(design)
	publish := obj["publish"]
	if publish == nil {
		publish = map[string]any{}
	}
	if err := validateFormSchema(publish, formSchemas["FormPublishing"], "publish", true); err != nil {
		return false, err
	}
	var designValue any = design
	if config != nil {
		if !config.Design.IsNull() && !config.Design.IsUnknown() {
			c, _ := decodeFormJSON(config.Design.ValueString())
			designValue = projectFormJSON(designValue, c, formSchemas["FormDesignRequest"])
		}
		if !config.Publish.IsNull() && !config.Publish.IsUnknown() {
			c, _ := decodeFormJSON(config.Publish.ValueString())
			publish = projectFormJSON(publish, c, formSchemas["FormPublishing"])
		}
	}
	model.Design = formJSONValue(designValue)
	model.Publish = formJSONValue(publish)
	return true, nil
}
func (r *formTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model formTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.client.Forms(ctx)
	if err == nil {
		// Optional/computed publishing keeps its last observed value in the plan.
		// Replay it explicitly: the API does not document reset/merge semantics when
		// a PUT omits publish, so omission must not accidentally unpublish a form.
		err = client.Put(ctx, formTemplatePath(model.ProjectID.ValueString(), model.ID.ValueString()), formTemplateBody(model), nil)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to update form template", err.Error())
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(rememberFormConfiguration(ctx, req.Config, model, resp.Private)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func (r *formTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model formTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	client, err := r.client.Forms(ctx)
	if err == nil {
		_, err = client.DeleteWithStatus(ctx, formTemplatePath(model.ProjectID.ValueString(), model.ID.ValueString()))
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to delete form template", err.Error())
	}
}
func (r *formTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || !formProjectPattern.MatchString(parts[0]) || !formIDPattern.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid Import ID", "Expected project_id/form_uuid (project ID or key, then template UUID).")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

// Read does not receive Terraform configuration. Persist the last applied JSON
// shape privately so omitted API defaults do not become accidental ownership.
func rememberFormConfiguration(ctx context.Context, config tfsdk.Config, model formTemplateModel, private interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}) diag.Diagnostics {
	var publishing formJSON
	diags := config.GetAttribute(ctx, path.Root("publish"), &publishing)
	if diags.HasError() {
		return diags
	}
	values := map[string]json.RawMessage{"design": json.RawMessage(model.Design.ValueString())}
	if !publishing.IsNull() && !publishing.IsUnknown() {
		values["publish"] = json.RawMessage(publishing.ValueString())
	}
	raw, err := json.Marshal(values)
	if err != nil {
		diags.AddError("Invalid Forms configuration", "Unable to store JSON configuration metadata.")
		return diags
	}
	return append(diags, private.SetKey(ctx, "form_configuration", raw)...)
}

// A just-created template may not be visible immediately. Keep the write's plan
// values and UUID; polling verifies visibility without treating a transient 404
// as deletion or trusting a partial index entry as the full template.
func waitForFormTemplate(ctx context.Context, client *atlassian.Client, model *formTemplateModel) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		found, err := readFormTemplate(ctx, client, model, nil)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("timed out waiting for the created template to become visible")
		case <-timer.C:
		}
	}
}
