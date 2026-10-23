package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestWorkflowSchemeUpdateGroupsIssueTypesByWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		assignments map[string]string
		want        []schemeWorkflowAssociation
	}{
		{
			name:        "all-use-default",
			assignments: map[string]string{"10302": "Generic", "10301": "Generic", "10300": "Generic"},
			want:        []schemeWorkflowAssociation{{WorkflowID: "workflow-generic", IssueTypeIDs: []string{"10300", "10301", "10302"}}},
		},
		{
			name:        "multiple-workflows",
			assignments: map[string]string{"10302": "Joiner", "10301": "Generic", "10300": "Generic"},
			want: []schemeWorkflowAssociation{
				{WorkflowID: "workflow-generic", IssueTypeIDs: []string{"10300", "10301"}},
				{WorkflowID: "workflow-joiner", IssueTypeIDs: []string{"10302"}},
			},
		},
		{
			name:        "empty-mappings",
			assignments: map[string]string{},
			want:        []schemeWorkflowAssociation{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var preflights, mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodPost || req.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch req.URL.Path {
				case "/rest/api/3/workflowscheme/read":
					var body struct {
						IDs []string `json:"workflowSchemeIds"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || !reflect.DeepEqual(body.IDs, []string{"10401"}) {
						t.Error("invalid scheme lookup")
					}
					w.Write([]byte(`[{"id":"10401","version":{"id":"v","versionNumber":1},"defaultWorkflow":{"id":"workflow-old"},"workflowsForIssueTypes":[]}]`))
				case "/rest/api/3/workflows":
					var body struct {
						Names []string `json:"workflowNames"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.Names) != 1 {
						t.Error("invalid workflow lookup")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					id := map[string]string{"Generic": "workflow-generic", "Joiner": "workflow-joiner"}[body.Names[0]]
					if id == "" {
						t.Errorf("unexpected workflow name %q", body.Names[0])
					}
					json.NewEncoder(w).Encode(map[string]any{"workflows": []any{map[string]string{"id": id, "name": body.Names[0]}}})
				case "/rest/api/3/workflowscheme/update/mappings", "/rest/api/3/workflowscheme/update":
					var body struct {
						ID           string                      `json:"id"`
						Default      string                      `json:"defaultWorkflowId"`
						Associations []schemeWorkflowAssociation `json:"workflowsForIssueTypes"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.ID != "10401" || body.Default != "workflow-generic" {
						t.Errorf("incorrect scheme identifiers: %+v", body)
					}
					seen := map[string]bool{}
					for _, association := range body.Associations {
						if seen[association.WorkflowID] {
							w.WriteHeader(http.StatusBadRequest)
							w.Write([]byte(`{"errorMessages":["Workflow must have a single association entry"],"errors":{}}`))
							return
						}
						seen[association.WorkflowID] = true
					}
					if !reflect.DeepEqual(body.Associations, tc.want) {
						t.Errorf("associations = %+v, want %+v", body.Associations, tc.want)
					}
					if req.URL.Path == "/rest/api/3/workflowscheme/update/mappings" {
						preflights.Add(1)
						w.Write([]byte(`{"statusMappingsByIssueTypes":[]}`))
					} else {
						if preflights.Load() != 1 {
							t.Error("update before mapping preflight")
						}
						mutations.Add(1)
						w.Write([]byte(`{}`))
					}
				default:
					t.Errorf("unexpected endpoint %s", req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			assignments, diags := types.MapValueFrom(ctx, types.StringType, tc.assignments)
			if diags.HasError() {
				t.Fatal(diags)
			}
			plan := workflowSchemeResourceModel{
				Name: types.StringValue("JML"), Description: types.StringValue("Managed"),
				DefaultWorkflow: types.StringValue("Generic"), IssueTypeMappings: assignments,
				StatusMappings: types.MapNull(types.MapType{ElemType: types.StringType}),
			}
			if err := updateModernScheme(ctx, client, "10401", plan); err != nil {
				t.Fatal(err)
			}
			if mutations.Load() != 1 {
				t.Fatalf("updates = %d, want 1", mutations.Load())
			}
		})
	}
}
