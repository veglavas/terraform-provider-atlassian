package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestWorkflowModernCreate(t *testing.T) {
	validated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/rest/api/3/status/1":
			json.NewEncoder(w).Encode(map[string]any{"id": "1", "name": "Open", "statusCategory": map[string]any{"key": "new"}})
		case "/rest/api/3/workflows/create/validation":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["payload"] == nil {
				t.Error("missing validation payload")
			}
			validated = true
			w.Write([]byte(`{"errors":[]}`))
		case "/rest/api/3/workflows/create":
			if !validated {
				t.Error("mutation before validation")
			}
			var body struct {
				Workflows []struct {
					Name        string           `json:"name"`
					Statuses    []map[string]any `json:"statuses"`
					Transitions []map[string]any `json:"transitions"`
				} `json:"workflows"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Workflows) != 1 || body.Workflows[0].Name != "test" || len(body.Workflows[0].Transitions) != 1 {
				t.Errorf("invalid create payload: %+v", body)
			}
			w.Write([]byte(`{"workflows":[{"id":"stable-id"}]}`))
		default:
			t.Errorf("unexpected endpoint %s %s", req.Method, req.URL.Path)
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
	if err != nil {
		t.Fatal(err)
	}
	r := &workflowResource{client: client}
	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	statuses, _ := types.ListValueFrom(ctx, types.StringType, []string{"1"})
	model := workflowResourceModel{ID: types.StringUnknown(), Name: types.StringValue("test"), Description: types.StringValue("description"), Statuses: statuses, Transitions: types.MapNull(types.ObjectType{AttrTypes: workflowTransitionTypes}), StatusMappings: types.MapNull(types.StringType)}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got workflowResourceModel
	resp.State.Get(ctx, &got)
	if got.ID.ValueString() != "stable-id" {
		t.Fatalf("ID = %s", got.ID)
	}
}
