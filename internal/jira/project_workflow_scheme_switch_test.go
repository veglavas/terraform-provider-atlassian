package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestProjectWorkflowSchemeSwitch(t *testing.T) {
	for _, scenario := range []string{"success", "already-assigned", "missing-mapping", "conflict", "failed-task", "wrong-association"} {
		t.Run(scenario, func(t *testing.T) {
			var assigned atomic.Int64
			assigned.Store(10)
			var switches atomic.Int32
			if scenario == "already-assigned" {
				assigned.Store(20)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/rest/api/3/workflowscheme/project":
					if r.Method != "GET" || r.URL.Query().Get("projectId") != "100" {
						t.Error("invalid association query")
					}
					json.NewEncoder(w).Encode(map[string]any{"values": []any{map[string]any{"workflowScheme": map[string]any{"id": assigned.Load()}}}})
				case "/rest/api/3/workflowscheme/read":
					fmt.Fprint(w, `[{"id":"20","version":{"id":"v","versionNumber":1},"defaultWorkflow":{"id":"workflow-new"},"workflowsForIssueTypes":[]}]`)
				case "/rest/api/3/workflowscheme/update/mappings":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body["id"] != "10" || body["defaultWorkflowId"] != "workflow-new" {
						t.Errorf("wrong mapping request: %+v", body)
					}
					fmt.Fprint(w, `{"statusMappingsByIssueTypes":[{"issueTypeId":"5","statusIds":["1"]}]}`)
				case "/rest/api/3/workflowscheme/project/switch":
					switches.Add(1)
					var body struct {
						Project  string               `json:"projectId"`
						Target   string               `json:"targetSchemeId"`
						Mappings []issueStatusMapping `json:"mappingsByIssueTypeOverride"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Project != "100" || body.Target != "20" || len(body.Mappings) != 1 || body.Mappings[0].IssueTypeID != "5" || len(body.Mappings[0].StatusMappings) != 1 || body.Mappings[0].StatusMappings[0] != (statusMigration{Old: "1", New: "2"}) {
						t.Errorf("wrong switch payload: %+v", body)
					}
					if scenario == "conflict" {
						w.WriteHeader(409)
						return
					}
					w.WriteHeader(303)
					fmt.Fprint(w, `{"id":"task"}`)
				case "/rest/api/3/task/task":
					if scenario == "failed-task" {
						fmt.Fprint(w, `{"status":"FAILED","message":"cannot migrate"}`)
						return
					}
					if scenario != "wrong-association" {
						assigned.Store(20)
					}
					fmt.Fprint(w, `{"status":"COMPLETE"}`)
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			mappings := types.MapNull(types.MapType{ElemType: types.StringType})
			if scenario != "missing-mapping" {
				mappings, _ = types.MapValueFrom(context.Background(), types.MapType{ElemType: types.StringType}, map[string]map[string]string{"5": {"1": "2"}})
			}
			r := &projectWorkflowSchemeResource{client: client}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = r.switchScheme(ctx, "100", "20", mappings)
			wantError := scenario != "success" && scenario != "already-assigned"
			if (err != nil) != wantError {
				t.Fatalf("error=%v", err)
			}
			expected := int32(1)
			if scenario == "already-assigned" || scenario == "missing-mapping" {
				expected = 0
			}
			if switches.Load() != expected {
				t.Errorf("switches=%d", switches.Load())
			}
		})
	}
}

func TestProjectWorkflowSchemeReadDrift(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if missing {
					w.WriteHeader(404)
					return
				}
				fmt.Fprint(w, `{"values":[{"workflowScheme":{"id":20}}]}`)
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
			state := tfsdk.State{Schema: sr.Schema}
			model := projectWorkflowSchemeResourceModel{ProjectID: types.StringValue("100"), WorkflowSchemeID: types.StringValue("10"), StatusMappings: types.MapNull(types.MapType{ElemType: types.StringType}), DeleteStatusMappings: types.MapNull(types.MapType{ElemType: types.StringType}), DeleteWorkflowSchemeID: types.StringNull()}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			response := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			if missing {
				if !response.State.Raw.IsNull() {
					t.Fatal("missing project not removed")
				}
				return
			}
			if response.State.Raw.IsNull() {
				t.Fatal("drift removed resource")
			}
			response.State.Get(ctx, &model)
			if model.WorkflowSchemeID.ValueString() != "20" {
				t.Fatal("external association not refreshed")
			}
		})
	}
}
