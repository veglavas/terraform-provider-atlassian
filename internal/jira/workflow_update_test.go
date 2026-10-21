package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestWorkflowModernUpdate(t *testing.T) {
	for _, scenario := range []string{"success", "validation-error", "conflict", "task-failed", "missing-version", "missing-transitions"} {
		t.Run(scenario, func(t *testing.T) {
			var mutations atomic.Int32
			var validations atomic.Int32
			transition := map[string]any{"id": "1", "name": "Create", "type": "INITIAL", "toStatusReference": "1", "links": []any{}, "properties": map[string]any{"plugin": "keep"}, "validators": []any{map[string]any{"ruleKey": "custom", "parameters": map[string]any{"nested": "keep"}}}, "conditions": map[string]any{"operation": "AND", "conditions": []any{}}}
			current := map[string]any{"id": "stable-id", "name": "test", "description": "before", "version": map[string]any{"id": "version-id", "versionNumber": float64(4)}, "statuses": []any{map[string]any{"statusReference": "1", "properties": map[string]any{"jira.issue.editable": "false"}, "layout": map[string]any{"x": float64(2), "y": float64(3)}}}, "transitions": []any{transition}}
			if scenario == "missing-version" {
				delete(current, "version")
			}
			if scenario == "missing-transitions" {
				delete(current, "transitions")
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/api/3/workflows":
					var request struct {
						IDs []string `json:"workflowIds"`
					}
					if r.Method != "POST" {
						t.Errorf("method = %s", r.Method)
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.IDs) != 1 || request.IDs[0] != "stable-id" {
						t.Error("incorrect workflow lookup")
					}
					json.NewEncoder(w).Encode(map[string]any{"workflows": []any{current}})
				case "/rest/api/3/status/1":
					json.NewEncoder(w).Encode(map[string]any{"id": "1", "name": "Open", "statusCategory": map[string]string{"key": "new"}})
				case "/rest/api/3/workflows/update/validation":
					validations.Add(1)
					var body struct {
						Payload map[string]any `json:"payload"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Payload == nil {
						t.Error("missing validation payload")
					}
					if scenario == "validation-error" {
						fmt.Fprint(w, `{"errors":[{"level":"ERROR","code":"MAPPING_REQUIRED","message":"Provide migration mappings"}]}`)
					} else {
						fmt.Fprint(w, `{"errors":[]}`)
					}
				case "/rest/api/3/workflows/update":
					mutations.Add(1)
					if validations.Load() != 1 {
						t.Error("mutation before validation")
					}
					var body struct {
						Workflows []map[string]any `json:"workflows"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body.Workflows) != 1 {
						t.Error("missing workflow")
						w.WriteHeader(400)
						return
					}
					got := body.Workflows[0]
					for _, key := range []string{"id", "version", "transitions", "statuses"} {
						if !reflect.DeepEqual(got[key], current[key]) {
							t.Errorf("%s not preserved: got %#v want %#v", key, got[key], current[key])
						}
					}
					if got["description"] != "after" {
						t.Error("description not updated")
					}
					if scenario == "conflict" {
						w.WriteHeader(409)
						fmt.Fprint(w, `{"errorMessages":["conflict"]}`)
						return
					}
					if scenario == "task-failed" {
						fmt.Fprint(w, `{"taskId":"task-1"}`)
					} else {
						fmt.Fprint(w, `{"workflows":[{"id":"stable-id"}]}`)
					}
				case "/rest/api/3/task/task-1":
					fmt.Fprint(w, `{"status":"FAILED","message":"Migration failed"}`)
				default:
					t.Errorf("unexpected endpoint %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
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
			statuses, _ := types.ListValueFrom(ctx, types.StringType, []string{"1"})
			model := workflowResourceModel{ID: types.StringValue("stable-id"), Name: types.StringValue("test"), Description: types.StringValue("before"), Statuses: statuses, Transitions: types.MapNull(types.ObjectType{AttrTypes: workflowTransitionTypes}), StatusMappings: types.MapNull(types.StringType)}
			state := tfsdk.State{Schema: schemaResp.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			model.Description = types.StringValue("after")
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
			if scenario == "success" {
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				var got workflowResourceModel
				resp.State.Get(ctx, &got)
				if got.ID.ValueString() != "stable-id" || got.Description.ValueString() != "after" {
					t.Fatalf("incorrect state: %+v", got)
				}
			} else if !resp.Diagnostics.HasError() {
				t.Fatal("expected diagnostic")
			}
			want := int32(1)
			if scenario == "validation-error" || strings.HasPrefix(scenario, "missing-") {
				want = 0
			}
			if mutations.Load() != want {
				t.Errorf("mutations = %d, want %d", mutations.Load(), want)
			}
		})
	}
}

func TestWorkflowTransitionMerge(t *testing.T) {
	ctx := context.Background()
	rule := rawJSON([]any{map[string]any{"ruleKey": "keep", "parameters": map[string]any{"foo": "bar"}}})
	workflow := workflowDefinition{"transitions": rawJSON([]workflowDefinition{
		{"id": rawJSON("1"), "name": rawJSON("Create"), "validators": rule},
		{"id": rawJSON("2"), "name": rawJSON("Unmanaged")},
		{"id": rawJSON("3"), "name": rawJSON("Remove")},
	})}
	payload := map[string]any{"workflows": []workflowDefinition{workflow}}
	transition := managedWorkflowTransition{Name: types.StringValue("Renamed"), Type: types.StringValue("INITIAL"), To: types.StringValue("1"), From: types.ListNull(types.StringType)}
	planned, d := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: workflowTransitionTypes}, map[string]managedWorkflowTransition{"1": transition})
	if d.HasError() {
		t.Fatal(d)
	}
	previous, d := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: workflowTransitionTypes}, map[string]managedWorkflowTransition{"1": transition, "3": transition})
	if d.HasError() {
		t.Fatal(d)
	}
	if err := applyWorkflowTransitions(ctx, payload, planned, previous); err != nil {
		t.Fatal(err)
	}
	var got []workflowDefinition
	if err := json.Unmarshal(workflow["transitions"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || definitionString(got[0], "name") != "Renamed" || definitionString(got[1], "id") != "2" || string(got[0]["validators"]) != string(rule) {
		t.Fatalf("incorrect merge: %s", workflow["transitions"])
	}
	refreshed, err := refreshWorkflowTransitions(ctx, planned, workflow)
	if err != nil {
		t.Fatal(err)
	}
	if !refreshed.Equal(planned) {
		t.Fatalf("unexpected drift: %s", refreshed)
	}
}
