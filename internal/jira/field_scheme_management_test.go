package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFieldSchemeManagementBulkContract(t *testing.T) {
	for _, tc := range []struct {
		name            string
		project, remove bool
		status          int
		body            string
		wantError       bool
	}{
		{"association success", false, false, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[10001],"success":true}]}`, false},
		{"association all work types", false, false, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[],"success":true}]}`, false},
		{"association all work types missing result types", false, false, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","success":true}]}`, true},
		{"association 204", false, false, 204, ``, false},
		{"association 207 success", false, false, 207, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[10001],"success":true}]}`, false},
		{"association failed", false, false, 207, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[10001],"success":false,"error":"PRIVATE_TENANT_TEXT"}]}`, true},
		{"association wrong scope", false, false, 200, `{"results":[{"schemeId":10002,"fieldId":"customfield_10000","workTypeIds":[10001],"success":true}]}`, true},
		{"association wrong work types", false, false, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[],"success":true}]}`, true},
		{"association missing success", false, false, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","workTypeIds":[10001]}]}`, true},
		{"association empty", false, false, 200, `{}`, true},
		{"association forbidden", false, false, 403, `PRIVATE_TENANT_TEXT`, true},
		{"remove success", false, true, 200, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","success":true}]}`, false},
		{"remove no content", false, true, 204, ``, false},
		{"remove failed", false, true, 207, `{"results":[{"schemeId":10000,"fieldId":"customfield_10000","success":false}]}`, true},
		{"project success", true, false, 200, `{"results":[{"schemeId":10000,"projectId":10002,"success":true}]}`, false},
		{"project no content", true, false, 204, ``, false},
		{"project failed", true, false, 207, `{"results":[{"schemeId":10000,"projectId":10002,"success":false,"error":"PRIVATE_TENANT_TEXT"}]}`, true},
		{"project wrong scope", true, false, 200, `{"results":[{"schemeId":10000,"projectId":10003,"success":true}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				apiPath, method, want := fieldSchemesPath+"/fields", "PUT", `{"customfield_10000":[{"schemeIds":[10000],"restrictedToWorkTypes":[10001]}]}`
				if strings.HasPrefix(tc.name, "association all") {
					want = `{"customfield_10000":[{"schemeIds":[10000]}]}`
				}
				if tc.project {
					apiPath = fieldSchemesPath + "/projects"
					want = `{"10000":{"projectIds":[10002]}}`
				}
				if tc.remove {
					method = "DELETE"
					want = `{"customfield_10000":{"schemeIds":[10000]}}`
				}
				if r.Method != method || r.URL.Path != apiPath || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				var actual, expected any
				if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(want), &expected); err != nil {
					t.Error(err)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Errorf("unscoped body: %v", actual)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			var err error
			if tc.project {
				err = assignFieldSchemeProject(context.Background(), client, "10000", "10002")
			} else {
				ids := []string{"10001"}
				if strings.HasPrefix(tc.name, "association all") {
					ids = nil
				}
				err = writeFieldSchemeAssociation(context.Background(), client, "10000", "customfield_10000", ids, tc.remove)
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE_TENANT_TEXT") {
				t.Fatal("echoed tenant response")
			}
		})
	}
}

func managementTestState(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(context.Background(), model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func TestFieldSchemeManagementReadState(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status             int
		body               string
		missing, wantError bool
	}{
		{"deleted", 404, ``, true, false},
		{"forbidden", 403, ``, false, true},
		{"malformed", 200, `{}`, false, true},
		{"wrong identity", 200, `{"id":"10001","name":"Other"}`, false, true},
		{"drift", 200, `{"id":"10000","name":"Changed","description":"Changed description"}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != fieldSchemesPath+"/10000" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			r := &fieldSchemeResource{client: client}
			state := managementTestState(t, r, fieldSchemeModel{types.StringValue("10000"), types.StringValue("Original"), types.StringValue("Original description")})
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("diagnostics=%v state=%v", resp.Diagnostics, resp.State.Raw)
			}
			if tc.wantError && !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("read error destroyed state")
			}
			if tc.name == "drift" {
				var got fieldSchemeModel
				resp.State.Get(context.Background(), &got)
				if got.Name.ValueString() != "Changed" || got.Description.ValueString() != "Changed description" {
					t.Fatal("metadata drift not read")
				}
			}
		})
	}
}
func TestFieldSchemeManagementCreateVerificationRetainsIdentity(t *testing.T) {
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			if r.URL.Path != fieldSchemesPath || r.URL.RawQuery != "" {
				t.Errorf("unexpected request %s", r.URL)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, map[string]string{"name": "Example", "description": ""}) {
				t.Error("unexpected POST body")
			}
			_, _ = w.Write([]byte(`{"id":10000}`))
		case "GET":
			if r.URL.Path != fieldSchemesPath+"/10000" || r.URL.RawQuery != "" {
				t.Errorf("unexpected request %s", r.URL)
			}
			w.WriteHeader(403)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	r := &fieldSchemeResource{client: client}
	state := managementTestState(t, r, fieldSchemeModel{types.StringUnknown(), types.StringValue("Example"), types.StringValue("")})
	resp := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing verification diagnostic")
	}
	var got fieldSchemeModel
	if d := resp.State.Get(context.Background(), &got); d.HasError() {
		t.Fatal(d)
	}
	if got.ID.ValueString() != "10000" {
		t.Fatal("lost successful POST identity")
	}
}
func TestFieldSchemeManagementDeleteStatus(t *testing.T) {
	for _, status := range []int{404, 403, 409} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "DELETE" || r.URL.Path != fieldSchemesPath+"/10000" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte("PRIVATE_TENANT_TEXT"))
			})
			r := &fieldSchemeResource{client: client}
			state := managementTestState(t, r, fieldSchemeModel{types.StringValue("10000"), types.StringValue("Example"), types.StringValue("")})
			resp := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != (status != 404) {
				t.Fatalf("diagnostics=%v", resp.Diagnostics)
			}
			if strings.Contains(fmt.Sprint(resp.Diagnostics), "PRIVATE_TENANT_TEXT") {
				t.Fatal("echoed tenant response")
			}
		})
	}
}
func TestFieldSchemeManagementProjectDestroyPolicy(t *testing.T) {
	var calls atomic.Int32
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		t.Errorf("retain policy made HTTP request %s", r.URL)
		w.WriteHeader(400)
	})
	r := &projectFieldSchemeResource{client: client}
	state := managementTestState(t, r, projectFieldSchemeModel{types.StringValue("10002"), types.StringValue("10000"), types.StringNull()})
	resp := resource.DeleteResponse{State: state}
	r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() || calls.Load() != 0 || resp.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("calls=%d diagnostics=%v", calls.Load(), resp.Diagnostics)
	}
}
func TestFieldSchemeManagementImportValidation(t *testing.T) {
	for _, tc := range []struct {
		r  resource.ResourceWithImportState
		id string
	}{
		{&fieldSchemeResource{}, "01"}, {&fieldSchemeResource{}, "0"}, {&fieldSchemeResource{}, "9223372036854775808"},
		{&projectFieldSchemeResource{}, "project-key"}, {&fieldSchemeFieldResource{}, "10000"}, {&fieldSchemeFieldResource{}, "10000:bad/field"}, {&fieldSchemeFieldResource{}, "01:summary"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			var schema resource.SchemaResponse
			tc.r.Schema(context.Background(), resource.SchemaRequest{}, &schema)
			resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema}}
			tc.r.ImportState(context.Background(), resource.ImportStateRequest{ID: tc.id}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("accepted invalid import ID")
			}
		})
	}
}
