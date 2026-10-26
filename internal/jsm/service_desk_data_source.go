package jsm

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

var _ datasource.DataSource = &serviceDeskDataSource{}

func NewServiceDeskDataSource() datasource.DataSource { return &serviceDeskDataSource{} }

type serviceDeskDataSource struct{ client *atlassian.Client }
type serviceDeskModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	ProjectKey  types.String `tfsdk:"project_key"`
	ProjectName types.String `tfsdk:"project_name"`
}
type serviceDeskAPI struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
}

func (d *serviceDeskDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jsm_service_desk"
}
func (d *serviceDeskDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Looks up the JSM service desk associated with a Jira project. Reference a managed service project's id to discover its service desk during apply, without manually supplying a service desk ID. Only desks visible to the authenticated user can be found. Retries absence for up to 30 seconds for newly provisioned service desks.", Attributes: map[string]schema.Attribute{
		"project_id":   schema.StringAttribute{Required: true, Description: "The numeric Jira project ID. Reference atlassian_jira_project.example.id for a managed project.", Validators: []validator.String{stringvalidator.RegexMatches(numericID, "must be a numeric Jira project ID")}},
		"id":           schema.StringAttribute{Computed: true, Description: "The numeric JSM service desk ID. Pass this to request types' service_desk_id."},
		"project_key":  schema.StringAttribute{Computed: true, Description: "The associated Jira project key."},
		"project_name": schema.StringAttribute{Computed: true, Description: "The associated Jira project name."},
	}}
}
func (d *serviceDeskDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// Scan all pages by exact project ID. Names and project IDs must never be
// confused with desk IDs, and list absence does not establish lack of access.
func findServiceDesk(ctx context.Context, client *atlassian.Client, projectID string) (*serviceDeskAPI, error) {
	start := 0
	var found *serviceDeskAPI
	seen := map[string]bool{}
	const maxPages = 1000
	for n := 0; n < maxPages; n++ {
		var page struct {
			Start      *int             `json:"start"`
			Size       *int             `json:"size"`
			IsLastPage *bool            `json:"isLastPage"`
			Values     []serviceDeskAPI `json:"values"`
		}
		apiPath := fmt.Sprintf("/rest/servicedeskapi/servicedesk?start=%d&limit=100", start)
		if err := client.Get(ctx, apiPath, &page); err != nil {
			return nil, err
		}
		if page.Start == nil || *page.Start != start || page.Size == nil || *page.Size != len(page.Values) || page.IsLastPage == nil || page.Values == nil {
			return nil, fmt.Errorf("incomplete or inconsistent service desk page at start=%d", start)
		}
		for _, desk := range page.Values {
			if !numericID.MatchString(desk.ID) || !numericID.MatchString(desk.ProjectID) || seen[desk.ID] {
				return nil, fmt.Errorf("invalid or repeated service desk in page at start=%d", start)
			}
			seen[desk.ID] = true
			if desk.ProjectID != projectID {
				continue
			}
			if found != nil {
				return nil, fmt.Errorf("multiple service desks matched project ID %s", projectID)
			}
			if desk.ProjectKey == "" || desk.ProjectName == "" {
				return nil, fmt.Errorf("incomplete service desk for project ID %s", projectID)
			}
			copy := desk
			found = &copy
		}
		if *page.IsLastPage {
			return found, nil
		}
		if len(page.Values) == 0 {
			return nil, fmt.Errorf("non-final service desk page at start=%d is empty", start)
		}
		start += len(page.Values)
	}
	return nil, fmt.Errorf("service desk pagination exceeded %d pages", maxPages)
}

func (d *serviceDeskDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config serviceDeskModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Terraform defers this read to apply when a managed project ID is unknown.
	// Never issue a broad lookup with an unresolved selector.
	if config.ProjectID.IsUnknown() || config.ProjectID.IsNull() {
		resp.Diagnostics.AddError("Unresolved project ID", "The service desk lookup requires a known Jira project ID.")
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		desk, err := findServiceDesk(waitCtx, d.client, config.ProjectID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading service desk", err.Error())
			return
		}
		if desk != nil {
			config.ID = types.StringValue(desk.ID)
			config.ProjectKey = types.StringValue(desk.ProjectKey)
			config.ProjectName = types.StringValue(desk.ProjectName)
			resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
			return
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			resp.Diagnostics.AddError("Service desk not found", fmt.Sprintf("No visible service desk was found for Jira project ID %s before the lookup deadline. Verify this is a provisioned service project and the authenticated user can access it. Newly created desks may need more time; retry the plan/apply. No request type was created by this lookup. %s", config.ProjectID.ValueString(), waitCtx.Err()))
			return
		case <-timer.C:
		}
	}
}
