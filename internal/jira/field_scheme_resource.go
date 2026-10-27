package jira

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ resource.ResourceWithImportState = &fieldSchemeResource{}

func NewFieldSchemeResource() resource.Resource { return &fieldSchemeResource{} }

type fieldSchemeResource struct{ client *atlassian.Client }
type fieldSchemeModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func (r *fieldSchemeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jira_field_scheme"
}
func (r *fieldSchemeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an importable Jira field association scheme using the experimental Field schemes API. Requires Administer Jira. Owns the scheme name and description, not its fields, requirements, or project assignments. Deleting an assigned or system scheme is rejected by Jira. Import existing schemes before managing them; shared schemes affect all assigned projects.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, Description: "Field scheme ID; import using this ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Scheme name.", Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"description": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Description: "Scheme description. Omission manages an empty description."},
		}}
}
func (r *fieldSchemeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*atlassian.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *atlassian.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}
func (r *fieldSchemeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fieldSchemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := saveManagedFieldScheme(ctx, r.client, "", plan.Name.ValueString(), plan.Description.ValueString())
	if id != "" {
		plan.ID = types.StringValue(id)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error creating field scheme", err.Error())
		return
	}
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying created field scheme", err.Error())
	}
}
func (r *fieldSchemeResource) wait(ctx context.Context, model fieldSchemeModel) error {
	return waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		s, err := readManagedFieldScheme(ctx, r.client, model.ID.ValueString())
		if err != nil || s == nil {
			return false, err
		}
		desc := ""
		if s.Description != nil {
			desc = *s.Description
		}
		return *s.Name == model.Name.ValueString() && desc == model.Description.ValueString(), nil
	})
}
func (r *fieldSchemeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fieldSchemeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := readManagedFieldScheme(ctx, r.client, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading field scheme", err.Error())
		return
	}
	if s == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Name = types.StringValue(*s.Name)
	state.Description = types.StringValue("")
	if s.Description != nil {
		state.Description = types.StringValue(*s.Description)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
func (r *fieldSchemeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan fieldSchemeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := saveManagedFieldScheme(ctx, r.client, plan.ID.ValueString(), plan.Name.ValueString(), plan.Description.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error updating field scheme", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err := r.wait(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Error verifying updated field scheme", err.Error())
	}
}
func (r *fieldSchemeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fieldSchemeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	status, err := r.client.DeleteWithStatus(ctx, fieldSchemesPath+"/"+atlassian.PathEscape(state.ID.ValueString()), atlassian.WithExperimentalAPI())
	if err != nil {
		if status != 0 {
			err = fmt.Errorf("field scheme DELETE returned HTTP %d; assigned and system schemes cannot be deleted", status)
		}
		resp.Diagnostics.AddError("Error deleting field scheme", err.Error())
		return
	}
	if status == http.StatusNotFound {
		return
	}
	if status != http.StatusOK {
		resp.Diagnostics.AddError("Unexpected field scheme deletion response", fmt.Sprintf("HTTP %d", status))
		return
	}
	if err := waitForFieldConfiguration(ctx, func(ctx context.Context) (bool, error) {
		s, err := readManagedFieldScheme(ctx, r.client, state.ID.ValueString())
		return s == nil, err
	}); err != nil {
		resp.Diagnostics.AddError("Error verifying field scheme deletion", err.Error())
	}
}
func (r *fieldSchemeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := positiveID(req.ID); err != nil {
		resp.Diagnostics.AddError("Invalid field scheme import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
