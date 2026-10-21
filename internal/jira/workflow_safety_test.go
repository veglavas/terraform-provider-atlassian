package jira

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestProjectWorkflowSchemeDeleteSafety(t *testing.T) {
	for _, scenario := range []string{"retain", "missing-project", "forbidden", "switch-conflict"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "retain" {
					t.Error("retain policy made an API call")
				}
				switch r.URL.Path {
				case "/rest/api/3/project/100":
					if scenario == "missing-project" {
						w.WriteHeader(404)
						return
					}
					if scenario == "forbidden" {
						w.WriteHeader(403)
						return
					}
					fmt.Fprint(w, `{"id":"100"}`)
				case "/rest/api/3/workflowscheme/project":
					fmt.Fprint(w, `{"values":[{"workflowScheme":{"id":10}}]}`)
				case "/rest/api/3/workflowscheme/read":
					fmt.Fprint(w, `[{"id":"20","defaultWorkflow":{"id":"workflow"},"workflowsForIssueTypes":[]}]`)
				case "/rest/api/3/workflowscheme/update/mappings":
					fmt.Fprint(w, `{"statusMappingsByIssueTypes":[]}`)
				case "/rest/api/3/workflowscheme/project/switch":
					w.WriteHeader(409)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			r := &projectWorkflowSchemeResource{client: client}
			ctx := context.Background()
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			model := projectWorkflowSchemeResourceModel{ProjectID: types.StringValue("100"), WorkflowSchemeID: types.StringValue("10"), StatusMappings: types.MapNull(types.MapType{ElemType: types.StringType}), DeleteStatusMappings: types.MapNull(types.MapType{ElemType: types.StringType}), DeleteWorkflowSchemeID: types.StringValue("20")}
			if scenario == "retain" {
				model.DeleteWorkflowSchemeID = types.StringNull()
			}
			state := tfsdk.State{Schema: sr.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			response := resource.DeleteResponse{State: state}
			r.Delete(ctx, resource.DeleteRequest{State: state}, &response)
			wantError := scenario == "forbidden" || scenario == "switch-conflict"
			if response.Diagnostics.HasError() != wantError {
				t.Fatalf("diagnostics: %s", response.Diagnostics)
			}
		})
	}
}

func TestWorkflowRemovedStatusesRequireExplicitMappings(t *testing.T) {
	ctx := context.Background()
	current := workflowDefinition{"statuses": rawJSON([]any{map[string]string{"statusReference": "old"}, map[string]string{"statusReference": "new"}})}
	if err := requireRemovedWorkflowMappings(ctx, current, []string{"new"}, types.MapNull(types.StringType)); err == nil {
		t.Fatal("status removal permitted without migration mapping")
	}
	mappings, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"old": "new"})
	if err := requireRemovedWorkflowMappings(ctx, current, []string{"new"}, mappings); err != nil {
		t.Fatal(err)
	}
	if err := requireRemovedWorkflowMappings(ctx, current, []string{"other"}, mappings); err == nil {
		t.Fatal("mapping to removed destination allowed")
	}
}

func TestWorkflowStatusOrder(t *testing.T) {
	ctx := context.Background()
	prior, _ := types.ListValueFrom(ctx, types.StringType, []string{"1", "2"})
	got := stableWorkflowIDs(ctx, prior, []string{"2", "3", "1"})
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("order = %v", got)
	}
}
