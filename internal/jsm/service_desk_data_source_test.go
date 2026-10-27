package jsm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func checkDeskListRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != "GET" || r.URL.Path != "/rest/servicedeskapi/servicedesk" || r.URL.Query().Get("limit") != "100" || len(r.URL.Query()) != 2 {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL)
	}
	user, token, ok := r.BasicAuth()
	if !ok || user != "test@example.org" || token != "test" {
		t.Error("missing auth")
	}
	if r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" {
		t.Error("missing user agent")
	}
}
func TestServiceDeskLookupPagination(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkDeskListRequest(t, r)
		switch r.URL.Query().Get("start") {
		case "0":
			_, _ = w.Write([]byte(`{"start":0,"size":1,"isLastPage":false,"values":[{"id":"1","projectId":"10001","projectKey":"OTHER","projectName":"Other"}]}`))
		case "1":
			_, _ = w.Write([]byte(`{"start":1,"size":1,"isLastPage":true,"values":[{"id":"2","projectId":"10002","projectKey":"SUPPORT","projectName":"Support"}]}`))
		default:
			t.Errorf("unexpected offset: %s", r.URL)
		}
	})
	desk, err := findServiceDesk(context.Background(), client, "10002")
	if err != nil || desk == nil || desk.ID != "2" || desk.ProjectID != "10002" || desk.ProjectKey != "SUPPORT" || desk.ProjectName != "Support" {
		t.Fatalf("desk=%+v err=%v", desk, err)
	}
}
func TestServiceDeskLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"absent", `{"start":0,"size":0,"isLastPage":true,"values":[]}`, 200, false},
		{"forbidden", `{}`, 403, true},
		{"404", `{}`, 404, true},
		{"malformed", `not json`, 200, true},
		{"missing pagination", `{"values":[]}`, 200, true},
		{"null values", `{"start":0,"size":0,"isLastPage":true,"values":null}`, 200, true},
		{"size mismatch", `{"start":0,"size":1,"isLastPage":true,"values":[]}`, 200, true},
		{"empty nonfinal", `{"start":0,"size":0,"isLastPage":false,"values":[]}`, 200, true},
		{"wrong offset", `{"start":1,"size":0,"isLastPage":true,"values":[]}`, 200, true},
		{"incomplete desk", `{"start":0,"size":1,"isLastPage":true,"values":[{"id":"2","projectId":"10002"}]}`, 200, true},
		{"missing project identity", `{"start":0,"size":1,"isLastPage":true,"values":[{"id":"2"}]}`, 200, true},
		{"ambiguous project", `{"start":0,"size":2,"isLastPage":true,"values":[{"id":"2","projectId":"10002","projectKey":"SUPPORT","projectName":"One"},{"id":"3","projectId":"10002","projectKey":"SUPPORT","projectName":"Two"}]}`, 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkDeskListRequest(t, r)
				if r.URL.Query().Get("start") != "0" {
					t.Errorf("offset: %s", r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			desk, err := findServiceDesk(context.Background(), client, "10002")
			if (err != nil) != tc.wantError || desk != nil {
				t.Fatalf("desk=%+v err=%v", desk, err)
			}
		})
	}
}
func TestServiceDeskRejectsRepeatedPages(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkDeskListRequest(t, r)
		_, _ = w.Write([]byte(`{"start":0,"size":1,"isLastPage":false,"values":[{"id":"2","projectId":"10002","projectKey":"SUPPORT","projectName":"Support"}]}`))
	})
	desk, err := findServiceDesk(context.Background(), client, "10002")
	if err == nil || desk != nil {
		t.Fatalf("partial scan accepted desk=%+v err=%v", desk, err)
	}
}
func TestServiceDeskReadVisibilityAndMissing(t *testing.T) {
	for _, visible := range []bool{true, false} {
		t.Run(fmt.Sprint(visible), func(t *testing.T) {
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkDeskListRequest(t, r)
				if r.URL.Query().Get("start") != "0" {
					t.Errorf("offset: %s", r.URL)
				}
				if calls.Add(1) == 1 || !visible {
					_, _ = w.Write([]byte(`{"start":0,"size":0,"isLastPage":true,"values":[]}`))
					return
				}
				_, _ = w.Write([]byte(`{"start":0,"size":1,"isLastPage":true,"values":[{"id":"2","projectId":"10002","projectKey":"SUPPORT","projectName":"Support"}]}`))
			})
			d := &serviceDeskDataSource{client: client}
			ctx := context.Background()
			var schema datasource.SchemaResponse
			d.Schema(ctx, datasource.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema}
			model := serviceDeskModel{ProjectID: types.StringValue("10002"), ID: types.StringNull(), ProjectKey: types.StringNull(), ProjectName: types.StringNull()}
			if diags := state.Set(ctx, &model); diags.HasError() {
				t.Fatal(diags)
			}
			timeout := time.Second
			if !visible {
				timeout = 20 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			resp := datasource.ReadResponse{State: state}
			d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: state.Raw}}, &resp)
			if resp.Diagnostics.HasError() == visible {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if visible {
				if diags := resp.State.Get(ctx, &model); diags.HasError() {
					t.Fatal(diags)
				}
				if model.ID.ValueString() != "2" || model.ProjectID.ValueString() != "10002" || model.ProjectKey.ValueString() != "SUPPORT" || model.ProjectName.ValueString() != "Support" || calls.Load() != 2 {
					t.Fatalf("model=%+v calls=%d", model, calls.Load())
				}
			} else if !strings.Contains(fmt.Sprint(resp.Diagnostics), "Service desk not found") {
				t.Fatalf("wrong error: %v", resp.Diagnostics)
			}
		})
	}
}
