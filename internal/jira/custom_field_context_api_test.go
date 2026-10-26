package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func contextTestClient(t *testing.T, handler http.HandlerFunc) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test", Token: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func contextTestModel() customFieldContextModel {
	return customFieldContextModel{ID: types.StringValue("100"), FieldID: types.StringValue("customfield_10000"), Name: types.StringValue("Original"), Description: types.StringValue("Original description"), ProjectIDs: types.SetValueMust(types.StringType, nil), IssueTypeIDs: types.SetValueMust(types.StringType, nil)}
}
func optionTestModel() customFieldOptionModel {
	return customFieldOptionModel{ID: types.StringValue("200"), FieldID: types.StringValue("customfield_10000"), ContextID: types.StringValue("100"), Value: types.StringValue("Original"), Disabled: types.BoolValue(false), ParentOptionID: types.StringNull()}
}

func TestFieldContextResourceMissingAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"404", 404, "", false}, {"absent", 200, `{"isLast":true,"values":[]}`, false}, {"forbidden", 403, `{}`, true}, {"malformed", 200, `not json`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("method %s", r.Method)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			ctx := context.Background()
			t.Run("context", func(t *testing.T) {
				r := &customFieldContextResource{client: client}
				var schema resource.SchemaResponse
				r.Schema(ctx, resource.SchemaRequest{}, &schema)
				state := tfsdk.State{Schema: schema.Schema}
				model := contextTestModel()
				if d := state.Set(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() != tc.wantError {
					t.Fatalf("diagnostics: %v", resp.Diagnostics)
				}
				if !tc.wantError && !resp.State.Raw.IsNull() {
					t.Fatal("missing context not removed")
				}
			})
			t.Run("option", func(t *testing.T) {
				r := &customFieldOptionResource{client: client}
				var schema resource.SchemaResponse
				r.Schema(ctx, resource.SchemaRequest{}, &schema)
				state := tfsdk.State{Schema: schema.Schema}
				model := optionTestModel()
				if d := state.Set(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() != tc.wantError {
					t.Fatalf("diagnostics: %v", resp.Diagnostics)
				}
				if !tc.wantError && !resp.State.Raw.IsNull() {
					t.Fatal("missing option not removed")
				}
			})
		})
	}
}

func TestFieldContextOptionDrift(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`{"isLast":true,"values":[{"id":"200","value":"Renamed","disabled":true,"optionId":"199"}]}`))
	})
	ctx := context.Background()
	r := &customFieldOptionResource{client: client}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := optionTestModel()
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if d := resp.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	if model.Value.ValueString() != "Renamed" || !model.Disabled.ValueBool() || model.ParentOptionID.ValueString() != "199" {
		t.Fatalf("drift not read: %+v", model)
	}
}

func TestFieldContextScopeDrift(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("contextId") != "100" || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		var value any
		switch {
		case strings.HasSuffix(r.URL.Path, "projectmapping"):
			value = map[string]any{"contextId": "100", "projectId": "10001"}
		case strings.HasSuffix(r.URL.Path, "issuetypemapping"):
			value = map[string]any{"contextId": "100", "issueTypeId": "10002"}
		default:
			value = map[string]any{"id": "100", "name": "Renamed", "description": "Changed", "isGlobalContext": false, "isAnyIssueType": false}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"isLast": true, "values": []any{value}})
	})
	ctx := context.Background()
	r := &customFieldContextResource{client: client}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := contextTestModel()
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if d := resp.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	projects, issues := []string{}, []string{}
	_ = model.ProjectIDs.ElementsAs(ctx, &projects, false)
	_ = model.IssueTypeIDs.ElementsAs(ctx, &issues, false)
	if model.Name.ValueString() != "Renamed" || model.Description.ValueString() != "Changed" || len(projects) != 1 || projects[0] != "10001" || len(issues) != 1 || issues[0] != "10002" {
		t.Fatalf("drift not read: %+v", model)
	}
}

func TestFieldContextDelete404(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method %s", r.Method)
		}
		w.WriteHeader(404)
	})
	ctx := context.Background()
	r := &customFieldContextResource{client: client}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	model := contextTestModel()
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	o := &customFieldOptionResource{client: client}
	o.Schema(ctx, resource.SchemaRequest{}, &schema)
	state = tfsdk.State{Schema: schema.Schema}
	om := optionTestModel()
	if d := state.Set(ctx, &om); d.HasError() {
		t.Fatal(d)
	}
	resp = resource.DeleteResponse{}
	o.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
}

func TestFieldContextInvalidImports(t *testing.T) {
	ctx := context.Background()
	for _, r := range []resource.Resource{NewCustomFieldContextResource(), NewCustomFieldOptionResource()} {
		var schema resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &schema)
		for _, id := range []string{"", "a", "/b", "a/", "a//b", "a/b/c/d"} {
			response := resource.ImportStateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: id}, &response)
			if !response.Diagnostics.HasError() {
				t.Errorf("%T accepted import %q", r, id)
			}
		}
	}
}

func TestFieldContextAmbiguousName(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"isLast":true,"values":[{"id":"1","name":"Duplicate"},{"id":"2","name":"Duplicate"}]}`))
	})
	_, err := findFieldContext(context.Background(), client, "customfield_10000", "", "Duplicate")
	if err == nil || !strings.Contains(err.Error(), "multiple contexts") {
		t.Fatalf("error = %v", err)
	}
}

func TestFieldContextPathEscaping(t *testing.T) {
	if got := fieldContextPath("customfield a/b", "c/d"); got != "/rest/api/3/field/customfield%20a%2Fb/context/c%2Fd" {
		t.Fatalf("path = %s", got)
	}
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("contextId") != "a/b +&" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"isLast":true,"values":[]}`))
	})
	if _, err := findFieldContext(context.Background(), client, "customfield_10000", "a/b +&", ""); err != nil {
		t.Fatal(err)
	}
}

func TestFieldContextPaginationRejectsRepeatingPage(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"isLast":false,"startAt":0,"values":[{"id":"1"}]}`))
	})
	_, _, err := fieldContextPages[fieldContextAPI](context.Background(), client, "/contexts")
	if err == nil || !strings.Contains(err.Error(), "expected 1") {
		t.Fatalf("error = %v", err)
	}
}

func TestFieldContextScopedMappingNotReady(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"isLast":true,"values":[]}`)) })
	_, _, missing, err := readFieldContextScope(context.Background(), client, "customfield_10000", fieldContextAPI{ID: "100", IsGlobal: false, IsAnyIssueType: true})
	if err == nil || missing {
		t.Fatalf("missing=%t err=%v", missing, err)
	}
}

func validateFieldConfigurationCreate(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.RawQuery != "" {
		t.Errorf("unexpected create request: %s %s", r.Method, r.URL)
	}
	var expected string
	switch r.URL.Path {
	case fieldContextsPath("customfield_10000"):
		expected = `{"name":"Original","description":"Original description","projectIds":[],"issueTypeIds":[]}`
	case fieldContextPath("customfield_10000", "100") + "/option":
		expected = `{"options":[{"value":"Original","disabled":false}]}`
	default:
		t.Errorf("unexpected create path: %s", r.URL)
		return
	}
	var got, want any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Errorf("invalid create body: %v", err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("create body = %v, want %v", got, want)
	}
}

func TestFieldContextMissingCreateIDs(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		validateFieldConfigurationCreate(t, r)
		_, _ = w.Write([]byte(`{}`))
	})
	ctx := context.Background()
	r := &customFieldContextResource{client: client}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	plan := tfsdk.Plan{Schema: schema.Schema}
	model := contextTestModel()
	model.ID = types.StringUnknown()
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing context ID accepted")
	}
	o := &customFieldOptionResource{client: client}
	o.Schema(ctx, resource.SchemaRequest{}, &schema)
	plan = tfsdk.Plan{Schema: schema.Schema}
	om := optionTestModel()
	om.ID = types.StringUnknown()
	if d := plan.Set(ctx, &om); d.HasError() {
		t.Fatal(d)
	}
	resp = resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	o.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("missing option ID accepted")
	}
}

func TestFieldConfigurationVisibilityWait(t *testing.T) {
	calls := 0
	err := waitForFieldConfiguration(context.Background(), func(context.Context) (bool, error) { calls++; return calls >= 2, nil })
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = waitForFieldConfiguration(ctx, func(context.Context) (bool, error) { return false, nil })
	if err == nil {
		t.Fatal("canceled wait succeeded")
	}
}

func TestFieldContextCreateRetainsIDOnVisibilityFailure(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			validateFieldConfigurationCreate(t, r)
			_, _ = w.Write([]byte(`{"id":"100","options":[{"id":"200"}]}`))
			return
		}
		if r.Method != http.MethodGet || r.URL.Query().Get("startAt") != "0" || r.URL.Query().Get("maxResults") != "100" {
			t.Errorf("unexpected visibility request: %s %s", r.Method, r.URL)
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
			t.Errorf("unexpected visibility path: %s", r.URL)
		}
		w.WriteHeader(http.StatusForbidden)
	})
	ctx := context.Background()
	r := &customFieldContextResource{client: client}
	var schema resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schema)
	plan := tfsdk.Plan{Schema: schema.Schema}
	model := contextTestModel()
	model.ID = types.StringUnknown()
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	response := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("visibility failure not reported")
	}
	if d := response.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	if model.ID.ValueString() != "100" {
		t.Fatal("created context ID lost")
	}
	o := &customFieldOptionResource{client: client}
	o.Schema(ctx, resource.SchemaRequest{}, &schema)
	plan = tfsdk.Plan{Schema: schema.Schema}
	om := optionTestModel()
	om.ID = types.StringUnknown()
	if d := plan.Set(ctx, &om); d.HasError() {
		t.Fatal(d)
	}
	response = resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
	o.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("visibility failure not reported")
	}
	if d := response.State.Get(ctx, &om); d.HasError() {
		t.Fatal(d)
	}
	if om.ID.ValueString() != "200" {
		t.Fatal("created option ID lost")
	}
}

func TestFieldContextWriteLock(t *testing.T) {
	client := contextTestClient(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	var wg sync.WaitGroup
	var active atomic.Int32
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := lockFieldContext(client, "/same-context")
			defer unlock()
			if n := active.Add(1); n != 1 {
				t.Errorf("concurrent writes = %d", n)
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()
	fieldContextLocks.Lock()
	defer fieldContextLocks.Unlock()
	if len(fieldContextLocks.entries) != 0 {
		t.Fatal(fmt.Sprintf("lock entries leaked: %v", fieldContextLocks.entries))
	}
}
