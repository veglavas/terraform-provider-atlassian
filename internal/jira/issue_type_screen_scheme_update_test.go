package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestIssueTypeScreenSchemeMappingUpdates(t *testing.T) {
	for _, scenario := range []string{"replace", "default", "combined", "default-failure", "remove-failure", "append-failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			before := map[string]string{"default": "10400", "10300": "10400", "10302": "10400", "10304": "10400"}
			wanted := map[string]string{"default": "10400", "10300": "10435", "10302": "10434", "10304": "10433"}
			if scenario == "default" {
				wanted = map[string]string{"default": "10499", "10300": "10400", "10302": "10400", "10304": "10400"}
			}
			if scenario == "combined" || scenario == "default-failure" {
				wanted = map[string]string{"default": "10499", "10300": "10435", "10302": "10400", "10500": "10436"}
			}
			current := make(map[string]string)
			for k, v := range before {
				current[k] = v
			}
			var mu sync.Mutex
			failed := false
			var events []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				base := "/rest/api/3/issuetypescreenscheme/10400"
				switch {
				case req.Method == http.MethodPut && req.URL.Path == base:
					var body issueTypeScreenSchemeUpdateRequest
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Name != "test" || body.Description != "description" {
						t.Error("incorrect metadata body")
						w.WriteHeader(400)
						return
					}
					w.WriteHeader(204)
				case req.Method == http.MethodPut && req.URL.Path == base+"/mapping/default":
					var body struct {
						ScreenSchemeID string `json:"screenSchemeId"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.ScreenSchemeID != wanted["default"] {
						t.Error("incorrect default body")
						w.WriteHeader(400)
						return
					}
					if scenario == "default-failure" && !failed {
						failed = true
						w.WriteHeader(400)
						fmt.Fprint(w, `{"errorMessages":["default update failed"]}`)
						return
					}
					current["default"] = body.ScreenSchemeID
					events = append(events, "default")
					w.WriteHeader(204)
				case req.Method == http.MethodPost && req.URL.Path == base+"/mapping/remove":
					var body struct {
						IDs []string `json:"issueTypeIds"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.IDs) == 0 {
						t.Error("incorrect remove body")
						w.WriteHeader(400)
						return
					}
					if scenario == "remove-failure" && !failed {
						failed = true
						w.WriteHeader(400)
						fmt.Fprint(w, `{"errorMessages":["remove failed"]}`)
						return
					}
					for _, id := range body.IDs {
						if id == "default" || current[id] == "" || wanted[id] == current[id] {
							t.Errorf("unexpected removal %q", id)
							w.WriteHeader(400)
							return
						}
						delete(current, id)
					}
					events = append(events, "remove")
					w.WriteHeader(204)
				case req.Method == http.MethodPut && req.URL.Path == base+"/mapping":
					var body struct {
						Mappings []issueTypeScreenSchemeMappingItem `json:"issueTypeMappings"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.Mappings) == 0 {
						t.Error("incorrect append body")
						w.WriteHeader(400)
						return
					}
					for _, m := range body.Mappings {
						if m.IssueTypeID == "default" || current[m.IssueTypeID] != "" {
							w.WriteHeader(400)
							fmt.Fprint(w, `{"errorMessages":["One or more issue type IDs are already mapped, an issue type ID can only be mapped once."],"errors":{}}`)
							return
						}
						if wanted[m.IssueTypeID] != m.ScreenSchemeID {
							t.Errorf("unexpected mapping %+v", m)
							w.WriteHeader(400)
							return
						}
					}
					if scenario == "append-failure" && !failed {
						failed = true
						w.WriteHeader(400)
						fmt.Fprint(w, `{"errorMessages":["append failed"]}`)
						return
					}
					for _, m := range body.Mappings {
						current[m.IssueTypeID] = m.ScreenSchemeID
					}
					events = append(events, "append")
					w.WriteHeader(204)
				case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issuetypescreenscheme":
					if req.URL.Query().Get("startAt") != "0" || req.URL.Query().Get("maxResults") != "50" {
						t.Errorf("incorrect scheme query %s", req.URL)
					}
					fmt.Fprint(w, `{"isLast":true,"values":[{"id":"10400","name":"test","description":"description"}]}`)
				case req.Method == http.MethodGet && req.URL.Path == "/rest/api/3/issuetypescreenscheme/mapping":
					if req.URL.Query().Get("issueTypeScreenSchemeId") != "10400" || req.URL.Query().Get("startAt") != "0" || req.URL.Query().Get("maxResults") != "50" {
						t.Errorf("incorrect mapping query %s", req.URL)
					}
					var values []issueTypeScreenSchemeMappingResponse
					for k, v := range current {
						values = append(values, issueTypeScreenSchemeMappingResponse{IssueTypeScreenSchemeID: "10400", IssueTypeID: k, ScreenSchemeID: v})
					}
					json.NewEncoder(w).Encode(map[string]any{"isLast": true, "values": values})
				default:
					t.Errorf("unexpected request %s %s", req.Method, req.URL)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
			if err != nil {
				t.Fatal(err)
			}
			r := &issueTypeScreenSchemeResource{client: client}
			var schema resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &schema)
			model := issueTypeScreenSchemeResourceModel{ID: types.StringValue("10400"), Name: types.StringValue("test"), Description: types.StringValue("description")}
			model.Mappings, _ = types.MapValueFrom(ctx, types.StringType, before)
			state := tfsdk.State{Schema: schema.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			model.Mappings, _ = types.MapValueFrom(ctx, types.StringType, wanted)
			plan := tfsdk.Plan{Schema: schema.Schema}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			response := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{State: state, Plan: plan}, &response)
			if scenario == "default-failure" || scenario == "remove-failure" || scenario == "append-failure" {
				if !response.Diagnostics.HasError() {
					t.Fatal("expected failure")
				}
				mu.Lock()
				if !failed {
					mu.Unlock()
					t.Fatalf("did not reach expected failure: %v", response.Diagnostics)
				}
				if scenario != "append-failure" && !reflect.DeepEqual(current, before) {
					t.Error("mapping changed despite failed removal")
				}
				mu.Unlock()
				read := resource.ReadResponse{State: response.State}
				r.Read(ctx, resource.ReadRequest{State: response.State}, &read)
				if read.Diagnostics.HasError() {
					t.Fatal(read.Diagnostics)
				}
				response = resource.UpdateResponse{State: read.State}
				r.Update(ctx, resource.UpdateRequest{State: read.State, Plan: plan}, &response)
			}
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			mu.Lock()
			if !reflect.DeepEqual(current, wanted) {
				t.Errorf("mappings=%v, want %v", current, wanted)
			}
			eventCount := len(events)
			mu.Unlock()
			var got issueTypeScreenSchemeResourceModel
			if d := response.State.Get(ctx, &got); d.HasError() {
				t.Fatal(d)
			}
			var gotMappings map[string]string
			if d := got.Mappings.ElementsAs(ctx, &gotMappings, false); d.HasError() {
				t.Fatal(d)
			}
			if !reflect.DeepEqual(gotMappings, wanted) {
				t.Errorf("state mappings=%v, want %v", gotMappings, wanted)
			}
			again := resource.UpdateResponse{State: response.State}
			r.Update(ctx, resource.UpdateRequest{State: response.State, Plan: plan}, &again)
			if again.Diagnostics.HasError() {
				t.Fatal(again.Diagnostics)
			}
			mu.Lock()
			if len(events) != eventCount {
				t.Errorf("unchanged mappings mutated: %v", events)
			}
			mu.Unlock()
		})
	}
}
