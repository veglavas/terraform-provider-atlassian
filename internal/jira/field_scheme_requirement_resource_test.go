package jira

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func fieldRequirementTestState(t *testing.T) (fieldSchemeRequirementModel, tfsdk.State) {
	t.Helper()
	var schema resource.SchemaResponse
	(&fieldSchemeRequirementResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schema)
	model := fieldSchemeRequirementModel{
		ID:       types.StringValue("10000:customfield_10000:10001"),
		SchemeID: types.StringValue("10000"), FieldID: types.StringValue("customfield_10000"),
		IssueTypeID: types.StringValue("10001"), IsRequired: types.BoolValue(true),
	}
	state := tfsdk.State{Schema: schema.Schema}
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	return model, state
}

func TestFieldSchemeRequirementReadState(t *testing.T) {
	for _, tc := range []struct {
		name, body                   string
		status                       int
		missing, wantError, required bool
	}{
		{"drift", `{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":false}}]}`, 200, false, false, false},
		{"parent deleted", ``, 404, true, false, false},
		{"association deleted", `{"startAt":0,"isLast":true,"values":[]}`, 200, true, false, false},
		{"permission error", ``, 403, false, true, true},
		{"malformed page", `{"values":[]}`, 200, false, true, true},
		{"omitted required", `{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000"}]}`, 200, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && tc.status == http.StatusNotFound {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields/customfield_10000/parameters" && tc.name == "omitted required" {
					_, _ = w.Write([]byte(`{"fieldId":"customfield_10000"}`))
					return
				}
				if r.Method != "GET" || r.URL.Path != fieldSchemesPath+"/10000/fields" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			model, state := fieldRequirementTestState(t)
			resp := resource.ReadResponse{State: state}
			(&fieldSchemeRequirementResource{client: client}).Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("state=%v diagnostics=%v", resp.State.Raw, resp.Diagnostics)
			}
			if !tc.missing {
				if d := resp.State.Get(context.Background(), &model); d.HasError() {
					t.Fatal(d)
				}
				if model.IsRequired.ValueBool() != tc.required {
					t.Fatalf("required=%v", model.IsRequired)
				}
			}
		})
	}
}

func TestFieldSchemeRequirementDeleteStatus(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		firstStatus, deleteStatus, secondStatus int
		wantError                               bool
	}{
		{"already missing", 404, 0, 0, false},
		{"removed", 200, 204, 0, false},
		{"parent disappears", 200, 404, 404, false},
		{"bulk API absent but parent exists", 200, 404, 200, true},
		{"forbidden", 200, 403, 200, true},
		{"read forbidden", 403, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, deletes atomic.Int32
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "GET":
					if r.URL.Path == fieldSchemesPath+"/10000" {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					status := tc.firstStatus
					if reads.Add(1) > 1 {
						status = tc.secondStatus
					}
					if status == 0 {
						t.Error("unexpected extra read")
						status = 400
					}
					w.WriteHeader(status)
					if status == 200 {
						_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":true}}]}`))
					}
				case "DELETE":
					deletes.Add(1)
					if r.URL.Path != fieldSchemesPath+"/fields/parameters" {
						t.Errorf("unexpected delete path %s", r.URL)
					}
					if tc.deleteStatus == 0 {
						t.Error("unexpected delete")
						w.WriteHeader(400)
						return
					}
					w.WriteHeader(tc.deleteStatus)
				default:
					t.Errorf("unexpected method %s", r.Method)
					w.WriteHeader(400)
				}
			})
			_, state := fieldRequirementTestState(t)
			resp := resource.DeleteResponse{State: state}
			(&fieldSchemeRequirementResource{client: client}).Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError {
				t.Fatalf("diagnostics=%v", resp.Diagnostics)
			}
			if tc.deleteStatus == 0 && deletes.Load() != 0 {
				t.Fatal("unnecessary delete")
			}
		})
	}
}

func TestFieldSchemeRequirementImport(t *testing.T) {
	for _, id := range []string{"", "1", "1:customfield_1", "1:customfield_1:2:3", "01:customfield_1:2", "1::2", "1:bad/field:2", "1:customfield_1:0", "9223372036854775808:customfield_1:2"} {
		t.Run(id, func(t *testing.T) {
			_, state := fieldRequirementTestState(t)
			resp := resource.ImportStateResponse{State: state}
			(&fieldSchemeRequirementResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: id}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("accepted invalid import ID")
			}
		})
	}
}

func TestFieldSchemeRequirementCreateVisibilityFailureKeepsIdentity(t *testing.T) {
	var puts atomic.Int32
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			puts.Add(1)
			w.WriteHeader(204)
		case "GET":
			_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":false}}]}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	})
	model, state := fieldRequirementTestState(t)
	model.ID = types.StringUnknown()
	if d := state.Set(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	resp := resource.CreateResponse{State: state}
	(&fieldSchemeRequirementResource{client: client}).Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || puts.Load() != 1 {
		t.Fatalf("puts=%d diagnostics=%v", puts.Load(), resp.Diagnostics)
	}
	if d := resp.State.Get(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	if model.ID.ValueString() != "10000:customfield_10000:10001" {
		t.Fatal("lost identity after accepted write")
	}
}

func TestFieldSchemeRequirementDataSourceMissing(t *testing.T) {
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	d := &fieldSchemeRequirementDataSource{client: client}
	var schema datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schema)
	model, _ := fieldRequirementTestState(t)
	state := tfsdk.State{Schema: schema.Schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatal(diags)
	}
	resp := datasource.ReadResponse{State: state}
	d.Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing field was not diagnosed")
	}
}

func TestFieldSchemeRequirementWaitEventualConsistency(t *testing.T) {
	var reads atomic.Int32
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected request %s", r.Method)
		}
		if r.URL.Path == fieldSchemesPath+"/10000" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n := reads.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		required := n > 2
		_, _ = fmt.Fprintf(w, `{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":%t}}]}`, required)
	})
	model, _ := fieldRequirementTestState(t)
	if err := (&fieldSchemeRequirementResource{client: client}).wait(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 3 {
		t.Fatalf("reads=%d", reads.Load())
	}
}
