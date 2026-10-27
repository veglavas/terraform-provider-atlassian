package jira

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestFieldRequirementOptionalSearchParameters(t *testing.T) {
	for _, tc := range []struct {
		name, params        string
		status              int
		required, wantError bool
	}{
		{"optional default", `{"fieldId":"customfield_10000","parameters":{"isRequired":false}}`, 200, false, false},
		{"required default", `{"fieldId":"customfield_10000","parameters":{"isRequired":true}}`, 200, true, false},
		{"type override", `{"fieldId":"customfield_10000","parameters":{"isRequired":true},"workTypeParameters":[{"workTypeId":10001,"isRequired":false}]}`, 200, false, false},
		{"missing effective value", `{"fieldId":"customfield_10000"}`, 200, false, true},
		{"missing work type required", `{"fieldId":"customfield_10000","parameters":{"isRequired":true},"workTypeParameters":[{"workTypeId":10001}]}`, 200, false, true},
		{"mismatched field", `{"fieldId":"summary","parameters":{"isRequired":false}}`, 200, false, true},
		{"parameter endpoint 404", ``, 404, false, true},
		{"parameter endpoint forbidden", ``, 403, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
					_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000"}]}`))
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields/customfield_10000/parameters" && r.URL.RawQuery == "":
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(tc.params))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			})
			required, found, err := readFieldRequirement(context.Background(), client, fieldRequirementKey{"10000", "customfield_10000", "10001"})
			if (err != nil) != tc.wantError || required != tc.required || found == tc.wantError {
				t.Fatalf("required=%v found=%v err=%v", required, found, err)
			}
		})
	}
}

func TestFieldSchemeRequirementUnavailablePreservesState(t *testing.T) {
	for _, tc := range []struct {
		name               string
		schemeStatus       int
		missing, wantError bool
	}{
		{"flag disabled", 200, false, true},
		{"scheme deleted", 404, true, false},
		{"cannot verify parent", 403, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
					w.WriteHeader(404)
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "":
					w.WriteHeader(tc.schemeStatus)
					if tc.schemeStatus == 200 {
						_, _ = w.Write([]byte(`{"id":"10000","name":"Existing scheme"}`))
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			})
			_, state := fieldRequirementTestState(t)
			resp := resource.ReadResponse{State: state}
			(&fieldSchemeRequirementResource{client: client}).Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != tc.wantError || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("state=%v diagnostics=%v", resp.State.Raw, resp.Diagnostics)
			}
			if tc.wantError && !resp.State.Raw.Equal(state.Raw) {
				t.Fatal("availability error changed state")
			}
		})
	}
}

func TestFieldRequirementMidPaginationUnavailable(t *testing.T) {
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=0&maxResults=100":
			_, _ = w.Write([]byte(`{"startAt":0,"isLast":false,"total":2,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":true}}]}`))
		case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000/fields" && r.URL.RawQuery == "fieldId=customfield_10000&startAt=1&maxResults=100":
			w.WriteHeader(404)
		case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "":
			_, _ = w.Write([]byte(`{"id":"10000","name":"Still exists"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	})
	_, found, err := readFieldRequirement(context.Background(), client, fieldRequirementKey{"10000", "customfield_10000", "10001"})
	if err == nil || found {
		t.Fatalf("accepted incomplete read: found=%v err=%v", found, err)
	}
}

func TestProjectFieldSchemeDisappearsDuringLookup(t *testing.T) {
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/projects" && r.URL.RawQuery == "projectId=10001&startAt=0&maxResults=100":
			_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":[{"projectId":10001,"schemeId":10000}]}`))
		case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "":
			w.WriteHeader(404)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(400)
		}
	})
	scheme, err := readProjectFieldScheme(context.Background(), client, "10001")
	if err == nil || scheme != nil {
		t.Fatalf("scheme=%v err=%v", scheme, err)
	}
}
