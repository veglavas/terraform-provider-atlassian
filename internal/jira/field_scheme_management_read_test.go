package jira

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFieldSchemeManagementAssociationReadState(t *testing.T) {
	for _, tc := range []struct {
		name, values         string
		status, parentStatus int
		missing, wantError   bool
	}{
		{"present all types", `[{"fieldId":"customfield_10000"}]`, 200, 0, false, false},
		{"present restricted", `[{"fieldId":"customfield_10000","restrictedToWorkTypes":["10001"]}]`, 200, 0, false, false},
		{"removed", `[]`, 200, 0, true, false},
		{"scheme deleted", ``, 404, 404, true, false},
		{"feature unavailable", ``, 404, 200, false, true},
		{"cannot verify parent", ``, 404, 403, false, true},
		{"forbidden", ``, 403, 0, false, true},
		{"duplicate fields", `[{"fieldId":"customfield_10000"},{"fieldId":"customfield_10000"}]`, 200, 0, false, true},
		{"wrong field", `[{"fieldId":"summary"}]`, 200, 0, false, true},
		{"invalid type ID", `[{"fieldId":"customfield_10000","restrictedToWorkTypes":["01"]}]`, 200, 0, false, true},
		{"duplicate types", `[{"fieldId":"customfield_10000","restrictedToWorkTypes":["10001","10001"]}]`, 200, 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("unexpected method %s", r.Method)
				}
				switch {
				case r.URL.Path == fieldSchemesPath+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
					w.WriteHeader(tc.status)
					if tc.status == 200 {
						_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":` + tc.values + `}`))
					}
				case r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "" && tc.parentStatus != 0:
					w.WriteHeader(tc.parentStatus)
					if tc.parentStatus == 200 {
						_, _ = w.Write([]byte(`{"id":"10000","name":"Existing"}`))
					}
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(400)
				}
			})
			r := &fieldSchemeFieldResource{client: client}
			ids, d := types.SetValueFrom(context.Background(), types.StringType, []string{"10001"})
			if d.HasError() {
				t.Fatal(d)
			}
			state := managementTestState(t, r, fieldSchemeFieldModel{types.StringValue("10000:customfield_10000"), types.StringValue("10000"), types.StringValue("customfield_10000"), ids})
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("diagnostics=%v state=%v", resp.Diagnostics, resp.State.Raw)
			}
			if tc.wantError && !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("read error destroyed state")
			}
			if tc.name == "present all types" {
				var got fieldSchemeFieldModel
				resp.State.Get(context.Background(), &got)
				if got.IssueTypeIDs.IsNull() || len(got.IssueTypeIDs.Elements()) != 0 {
					t.Fatalf("unrestricted field must read as an empty set: %v", got.IssueTypeIDs)
				}
			}
		})
	}
}

func TestFieldSchemeManagementProjectReadState(t *testing.T) {
	for _, tc := range []struct {
		name, values       string
		status             int
		missing, wantError bool
	}{
		{"drift", `[{"projectId":10002,"schemeId":90000}]`, 200, false, false},
		{"removed", `[]`, 200, true, false},
		{"feature unavailable", ``, 404, false, true},
		{"forbidden", ``, 403, false, true},
		{"duplicate mapping", `[{"projectId":10002,"schemeId":10000},{"projectId":10002,"schemeId":90000}]`, 200, false, true},
		{"wrong project", `[{"projectId":10003,"schemeId":10000}]`, 200, false, true},
		{"invalid scheme", `[{"projectId":10002,"schemeId":0}]`, 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != fieldSchemesPath+"/projects" || r.URL.RawQuery != "projectId=10002&startAt=0&maxResults=100" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":` + tc.values + `}`))
				}
			})
			r := &projectFieldSchemeResource{client: client}
			state := managementTestState(t, r, projectFieldSchemeModel{types.StringValue("10002"), types.StringValue("10000"), types.StringValue("80000")})
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("diagnostics=%v state=%v", resp.Diagnostics, resp.State.Raw)
			}
			if tc.wantError && !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("read error destroyed state")
			}
			if !tc.wantError && !tc.missing {
				var got projectFieldSchemeModel
				resp.State.Get(context.Background(), &got)
				if got.SchemeID.ValueString() != "90000" || got.OnDestroySchemeID.ValueString() != "80000" {
					t.Fatal("did not read drift or preserve local policy")
				}
			}
		})
	}
}
