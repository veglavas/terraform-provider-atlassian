package jsm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func testClient(t *testing.T, h http.HandlerFunc) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test@example.org", Token: "test", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func testModel() requestTypeModel {
	return requestTypeModel{ID: types.StringValue("10"), ServiceDeskID: types.StringValue("2"), IssueTypeID: types.StringValue("10001"), Name: types.StringValue("Original"), Description: types.StringValue("Description"), HelpText: types.StringValue("Help"), PortalID: types.StringValue("3"), GroupIDs: types.SetValueMust(types.StringType, nil)}
}
func testState(t *testing.T, r *requestTypeResource) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := testModel()
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	return state
}
func checkRequest(t *testing.T, r *http.Request, method, path string, experimental bool) {
	t.Helper()
	if r.Method != method || r.URL.EscapedPath() != path || r.URL.RawQuery != "" {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL)
	}
	user, token, ok := r.BasicAuth()
	if !ok || user != "test@example.org" || token != "test" {
		t.Error("missing authentication")
	}
	if r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" {
		t.Error("missing user agent")
	}
	if experimental && r.Header.Get("X-ExperimentalApi") != "opt-in" {
		t.Error("missing experimental opt-in")
	}
}
func TestRequestTypeReadErrorsAndDrift(t *testing.T) {
	for _, tc := range []struct {
		name               string
		status             int
		body               string
		wantError, removed bool
	}{
		{"404", 404, "", false, true},
		{"forbidden", 403, `{}`, true, false},
		{"invalid json", 200, `broken`, true, false},
		{"incomplete", 200, `{}`, true, false},
		{"omitted description", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10001","name":"Original","portalId":"3","helpText":"Help"}`, true, false},
		{"null description", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10001","name":"Original","portalId":"3","description":null,"helpText":"Help"}`, true, false},
		{"omitted help text", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10001","name":"Original","portalId":"3","description":"Description"}`, true, false},
		{"null help text", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10001","name":"Original","portalId":"3","description":"Description","helpText":null}`, true, false},
		{"cleared metadata", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10001","name":"Original","portalId":"3","description":"","helpText":""}`, false, false},
		{"wrong object", 200, `{"id":"11","serviceDeskId":"2","issueTypeId":"10001","name":"Other","portalId":"3"}`, true, false},
		{"wrong desk", 200, `{"id":"10","serviceDeskId":"9","issueTypeId":"10001","name":"Other","portalId":"3"}`, true, false},
		{"drift", 200, `{"id":"10","serviceDeskId":"2","issueTypeId":"10002","name":"Renamed","description":"Changed","helpText":"New help","portalId":"4","groupIds":["8","9"]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkRequest(t, r, "GET", requestTypePath("2", "10"), false)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			ctx := context.Background()
			r := &requestTypeResource{client: client}
			state := testState(t, r)
			resp := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if resp.State.Raw.IsNull() != tc.removed {
				t.Fatalf("removed=%t", resp.State.Raw.IsNull())
			}
			if tc.wantError && !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("state changed on error")
			}
			if tc.name == "cleared metadata" {
				var model requestTypeModel
				if d := resp.State.Get(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				if model.Description.ValueString() != "" || model.HelpText.ValueString() != "" {
					t.Fatal("explicitly cleared metadata not read")
				}
			}
			if tc.name == "drift" {
				var model requestTypeModel
				if d := resp.State.Get(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				if model.Name.ValueString() != "Renamed" || model.Description.ValueString() != "Changed" || model.HelpText.ValueString() != "New help" || model.IssueTypeID.ValueString() != "10002" || model.PortalID.ValueString() != "4" || len(model.GroupIDs.Elements()) != 2 {
					t.Fatalf("drift not read: %+v", model)
				}
			}
		})
	}
}
func TestRequestTypeDelete(t *testing.T) {
	for _, status := range []int{204, 404, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkRequest(t, r, "DELETE", requestTypePath("2", "10"), true)
				if r.ContentLength > 0 {
					t.Error("unexpected delete body")
				}
				w.WriteHeader(status)
			})
			r := &requestTypeResource{client: client}
			state := testState(t, r)
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != (status == 403) {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
		})
	}
}
func TestRequestTypeImport(t *testing.T) {
	ctx := context.Background()
	r := &requestTypeResource{}
	for _, id := range []string{"", "2", "/10", "2/", "2/10/3", "JML/10", "2/abc", "2/10"} {
		t.Run(id, func(t *testing.T) {
			resp := resource.ImportStateResponse{State: testState(t, r)}
			r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
			if resp.Diagnostics.HasError() != (id != "2/10") {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if id == "2/10" {
				var desk, request types.String
				resp.State.GetAttribute(ctx, path.Root("service_desk_id"), &desk)
				resp.State.GetAttribute(ctx, path.Root("id"), &request)
				if desk.ValueString() != "2" || request.ValueString() != "10" {
					t.Fatal("wrong import state")
				}
			}
		})
	}
}
func TestRequestTypeCreateFailureRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		retained   bool
	}{
		{"missing ID", `{}`, 200, false},
		{"forbidden", `{}`, 403, false},
		{"visibility failure", `{"id":"10"}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					checkRequest(t, r, "POST", requestTypesPath("2"), true)
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 4 || body["name"] != "Original" || body["description"] != "Description" || body["helpText"] != "Help" || body["issueTypeId"] != "10001" {
						t.Errorf("body: %v", body)
					}
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				checkRequest(t, r, "GET", requestTypePath("2", "10"), false)
				w.WriteHeader(403)
			})
			ctx := context.Background()
			r := &requestTypeResource{client: client}
			state := testState(t, r)
			plan := tfsdk.Plan{Schema: state.Schema}
			model := testModel()
			model.ID = types.StringUnknown()
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("failure not reported")
			}
			if tc.retained {
				if d := resp.State.Get(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				if model.ID.ValueString() != "10" {
					t.Fatal("created ID lost")
				}
			}
		})
	}
}
func TestRequestTypeDataSourceMissing(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r, "GET", requestTypePath("2", "10"), false)
		w.WriteHeader(404)
	})
	ctx := context.Background()
	d := &requestTypeDataSource{client: client}
	var schema datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := testModel()
	if ds := state.Set(ctx, &model); ds.HasError() {
		t.Fatal(ds)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing data source not reported")
	}
}
func TestRequestTypePathEscaping(t *testing.T) {
	if got := requestTypePath("a /b", "c/d"); got != "/rest/servicedeskapi/servicedesk/a%20%2Fb/requesttype/c%2Fd" {
		t.Fatalf("path=%s", got)
	}
}
