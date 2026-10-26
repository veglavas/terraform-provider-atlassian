package jira

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

func TestFieldContextIncompleteCollectionsPreserveState(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"isLast":true}`,
		`{"isLast":true,"values":null}`,
		`{"values":[]}`,
		`{"isLast":false,"total":2,"values":[]}`,
		`{"isLast":true,"total":2,"values":[]}`,
		`{"isLast":true,"total":0,"values":[{"id":"100"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				switch r.URL.Path {
				case fieldContextsPath("customfield_10000"):
					if r.URL.Query().Get("contextId") != "100" {
						t.Errorf("missing context filter: %s", r.URL)
					}
				case fieldContextPath("customfield_10000", "100") + "/option":
					if r.URL.Query().Has("optionId") {
						t.Errorf("unexpected option filter: %s", r.URL)
					}
				default:
					t.Errorf("unexpected path: %s", r.URL)
				}
				_, _ = w.Write([]byte(body))
			})
			ctx := context.Background()
			for _, tc := range []struct {
				r     resource.Resource
				model any
			}{
				{&customFieldContextResource{client: client}, contextTestModel()},
				{&customFieldOptionResource{client: client}, optionTestModel()},
			} {
				var schema resource.SchemaResponse
				tc.r.Schema(ctx, resource.SchemaRequest{}, &schema)
				state := tfsdk.State{Schema: schema.Schema}
				if d := state.Set(ctx, tc.model); d.HasError() {
					t.Fatal(d)
				}
				resp := resource.ReadResponse{State: state}
				tc.r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatalf("%T accepted incomplete response", tc.r)
				}
				if !resp.State.Raw.Equal(state.Raw) {
					t.Fatalf("%T changed state after incomplete response", tc.r)
				}
			}
		})
	}
}

func TestFieldContextPaginationRejectsIncompleteLaterPage(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/contexts" || r.URL.Query().Get("maxResults") != "100" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		switch r.URL.Query().Get("startAt") {
		case "0":
			_, _ = w.Write([]byte(`{"startAt":0,"isLast":false,"total":2,"values":[{"id":"100"}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"startAt":1,"isLast":false,"total":2,"values":[]}`))
		default:
			t.Errorf("unexpected offset: %s", r.URL)
		}
	})
	values, _, err := fieldContextPages[fieldContextAPI](context.Background(), client, "/contexts")
	if err == nil || values != nil {
		t.Fatalf("incomplete scan returned values=%v err=%v", values, err)
	}
}
