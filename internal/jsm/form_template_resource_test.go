package jsm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

const formTestID = "c18bde7a-d846-11ed-afa1-0242ac120002"
const formTestDesign = `{"conditions":{},"layout":[{}],"questions":{"1":{"type":"ts","label":"Employee","jiraField":"customfield_10000","validation":{"rq":true}}},"sections":{},"settings":{"name":"Joiner","submit":{"lock":true,"pdf":false}}}`
const formTestPublish = `{"portal":{"portalRequestTypeIds":[63],"submitOnCreate":true,"validateOnCreate":true}}`

type formTestTransport func(*http.Request) (*http.Response, error)

func (f formTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockFormClient(t *testing.T, h http.HandlerFunc) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("unexpected query")
		}
		if r.URL.Path == "/_edge/tenant_info" {
			_, _ = io.WriteString(w, `{"cloudId":"33398233-27d5-48f6-9dea-d0ea33d97528"}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: "https://forms-unit.atlassian.net", User: "test", Token: "secret", HTTPClient: &http.Client{Transport: formTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "forms-unit.atlassian.net" && r.URL.Host != "api.atlassian.com" {
			return nil, fmt.Errorf("test blocked non-mock destination")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = target.Scheme
		u.Host = target.Host
		clone.URL = &u
		return server.Client().Transport.RoundTrip(clone)
	})}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func formTestModel() formTemplateModel {
	return formTemplateModel{ID: types.StringValue(formTestID), ProjectID: types.StringValue("JML"), Design: formJSON{types.StringValue(formTestDesign)}, Publish: formJSON{types.StringValue(formTestPublish)}}
}
func formTestState(t *testing.T, r *formTemplateResource) tfsdk.State {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &sr)
	s := tfsdk.State{Schema: sr.Schema}
	m := formTestModel()
	if d := s.Set(t.Context(), &m); d.HasError() {
		t.Fatal(d)
	}
	return s
}
func TestFormTemplateReadAndDataSource(t *testing.T) {
	valid := fmt.Sprintf(`{"id":%q,"design":%s,"publish":%s}`, formTestID, formTestDesign, formTestPublish)
	for _, tc := range []struct {
		name         string
		status       int
		body         string
		bad, missing bool
	}{
		{"valid", 200, valid, false, false},
		{"missing", 404, "", false, true},
		{"forbidden", 403, `{"echo":"secret"}`, true, false},
		{"invalid JSON", 200, "bad", true, false},
		{"missing design", 200, `{"id":"` + formTestID + `"}`, true, false},
		{"mismatched ID", 200, strings.Replace(valid, formTestID, "other", 1), true, false},
		{"truncated design", 200, `{"id":"` + formTestID + `","design":{}}`, true, false},
		{"drift", 200, strings.Replace(valid, "Employee", "Changed", 1), false, false},
		{"omitted publish", 200, fmt.Sprintf(`{"id":%q,"design":%s}`, formTestID, formTestDesign), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := mockFormClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/project/JML/form/"+formTestID) {
					t.Error("wrong read")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			r := &formTemplateResource{client: c}
			s := formTestState(t, r)
			resp := resource.ReadResponse{State: s}
			r.Read(t.Context(), resource.ReadRequest{State: s}, &resp)
			if resp.Diagnostics.HasError() != tc.bad || resp.State.Raw.IsNull() != tc.missing {
				t.Fatalf("diagnostics=%v removed=%t", resp.Diagnostics, resp.State.Raw.IsNull())
			}
			if tc.bad && !resp.State.Raw.Equal(s.Raw) {
				t.Error("error changed state")
			}
			if strings.Contains(fmt.Sprint(resp.Diagnostics), "secret") {
				t.Error("credential leaked")
			}
			if tc.name == "drift" {
				var got formTemplateModel
				resp.State.Get(t.Context(), &got)
				if !strings.Contains(got.Design.ValueString(), "Changed") {
					t.Fatal("drift not read")
				}
			}
			ds := &formTemplateDataSource{client: c}
			var sr datasource.SchemaResponse
			ds.Schema(t.Context(), datasource.SchemaRequest{}, &sr)
			state := tfsdk.State{Schema: sr.Schema}
			model := formTestModel()
			state.Set(t.Context(), &model)
			dr := datasource.ReadResponse{State: state}
			ds.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: state.Raw}}, &dr)
			if dr.Diagnostics.HasError() != (tc.bad || tc.missing) {
				t.Fatal(dr.Diagnostics)
			}
		})
	}
}
func TestFormTemplateWritesAndPartialPreservation(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		for _, status := range []int{200, 404, 403, 412} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				client := mockFormClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method == "GET" && method == "POST" && status == 200 {
						fmt.Fprintf(w, `{"id":%q,"design":%s,"publish":%s}`, formTestID, formTestDesign, formTestPublish)
						return
					}
					if r.Method != method {
						t.Errorf("unexpected %s", r.Method)
					}
					expected := "/project/JML/form"
					if method != "POST" {
						expected += "/" + formTestID
					}
					if !strings.HasSuffix(r.URL.Path, expected) {
						t.Error("wrong write path")
					}
					if method != "DELETE" {
						var body map[string]json.RawMessage
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						if len(body) != 2 {
							t.Error("invalid write envelope")
						}
						for k, v := range map[string]string{"design": formTestDesign, "publish": formTestPublish} {
							a, _ := decodeFormJSON(string(body[k]))
							b, _ := decodeFormJSON(v)
							if !reflect.DeepEqual(a, b) {
								t.Errorf("wrong %s", k)
							}
						}
					}
					w.WriteHeader(status)
					fmt.Fprintf(w, `{"id":%q}`, formTestID)
				})
				r := &formTemplateResource{client: client}
				state := formTestState(t, r)
				plan := tfsdk.Plan{Schema: state.Schema, Raw: state.Raw}
				switch method {
				case "POST":
					resp := resource.CreateResponse{State: tfsdk.State{Schema: state.Schema}}
					r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &resp)
					if resp.Diagnostics.HasError() != (status != 200) {
						t.Fatal(resp.Diagnostics)
					}
					if status == 200 && !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("partial response lost plan")
					}
				case "PUT":
					resp := resource.UpdateResponse{State: state}
					r.Update(t.Context(), resource.UpdateRequest{Plan: plan}, &resp)
					if resp.Diagnostics.HasError() != (status != 200) {
						t.Fatal(resp.Diagnostics)
					}
					if !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("partial update changed plan")
					}
				case "DELETE":
					resp := resource.DeleteResponse{}
					r.Delete(t.Context(), resource.DeleteRequest{State: state}, &resp)
					if resp.Diagnostics.HasError() != (status != 200 && status != 404) {
						t.Fatal(resp.Diagnostics)
					}
				}
			})
		}
	}
}
func TestFormTemplateInvalidCreateAndImport(t *testing.T) {
	for _, body := range []string{`{}`, `{"id":"bad"}`, `invalid`} {
		t.Run(body, func(t *testing.T) {
			c := mockFormClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Error("unexpected call")
				}
				_, _ = io.WriteString(w, body)
			})
			r := &formTemplateResource{client: c}
			s := formTestState(t, r)
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
			r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan{Schema: s.Schema, Raw: s.Raw}}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("bad create accepted")
			}
		})
	}
	r := &formTemplateResource{}
	for _, id := range []string{"", "JML", "JML/not-a-uuid", "JML/" + formTestID + "/extra", "../" + formTestID, "JML/" + formTestID, "10001/" + formTestID} {
		resp := resource.ImportStateResponse{State: formTestState(t, r)}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &resp)
		valid := id == "JML/"+formTestID || id == "10001/"+formTestID
		if resp.Diagnostics.HasError() == valid {
			t.Fatalf("%s: %v", id, resp.Diagnostics)
		}
	}
}
func TestFormJSONSemanticEquality(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		equal bool
	}{
		{`{"a":1,"b":2}`, " { \"b\":2, \"a\":1.0 } ", true},
		{`[1,2]`, `[2,1]`, false}, {`{"a":1}`, `{"a":"1"}`, false}, {`{}`, `{"x":null}`, false}, {`bad`, `bad`, false},
	} {
		a := formJSON{types.StringValue(tc.a)}
		b := formJSON{types.StringValue(tc.b)}
		got, d := a.StringSemanticEquals(context.Background(), b)
		if d.HasError() || got != tc.equal {
			t.Fatalf("%s / %s = %t", tc.a, tc.b, got)
		}
	}
	for _, v := range []basetypes.StringValue{types.StringNull(), types.StringUnknown(), types.StringValue(formTestDesign)} {
		raw, _ := v.ToTerraformValue(t.Context())
		converted, err := (formJSONType{}).ValueFromTerraform(t.Context(), raw)
		if err != nil || !converted.Equal(formJSON{v}) {
			t.Fatal("custom type round trip", err)
		}
	}
}
func TestFormSchemaValidationAndProjection(t *testing.T) {
	integral, _ := decodeFormJSON(strings.Replace(formTestPublish, "[63]", "[63.0]", 1))
	if err := validateFormSchema(integral, formSchemas["FormPublishing"], "publish", false); err != nil {
		t.Fatal("JSON numeric integer representation rejected", err)
	}

	valid, _ := decodeFormJSON(formTestDesign)
	if err := validateFormSchema(valid, formSchemas["FormDesignRequest"], "design", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`null`, `[]`, `{}`, strings.Replace(formTestDesign, `"ts"`, `"text"`, 1), strings.Replace(formTestDesign, `"validation":{"rq":true}`, `"validation":{"rq":"true"}`, 1), strings.Replace(formTestDesign, `"layout":[{}]`, `"layout":{}`, 1), strings.Replace(formTestDesign, `"jiraField":"customfield_10000"`, `"jiraField":10000`, 1)} {
		v, _ := decodeFormJSON(bad)
		if err := validateFormSchema(v, formSchemas["FormDesignRequest"], "design", false); err == nil {
			t.Errorf("accepted invalid design %s", bad)
		}
	}
	for _, bad := range []string{`{"portal":{"portalRequestTypeIds":["63"],"submitOnCreate":true,"validateOnCreate":true}}`, `{"portal":{"portalRequestTypeIds":[63]}}`, `{"portal":{"portalRequestTypeIds":[63.1],"submitOnCreate":true,"validateOnCreate":true}}`} {
		v, _ := decodeFormJSON(bad)
		if err := validateFormSchema(v, formSchemas["FormPublishing"], "publish", false); err == nil {
			t.Error("invalid publish accepted")
		}
	}
	response, _ := decodeFormJSON(formTestDesign)
	settings := response.(map[string]any)["settings"].(map[string]any)
	settings["language"] = "en"
	settings["primaryLocale"] = nil
	if err := validateFormSchema(response, formSchemas["FormDesign"], "design", true); err != nil {
		t.Fatal("API-managed optional metadata rejected", err)
	}
	projected := projectFormJSON(response, valid, formSchemas["FormDesignRequest"])
	if !reflect.DeepEqual(projected, valid) {
		t.Fatal("API defaults create drift")
	}
	response.(map[string]any)["questions"].(map[string]any)["2"] = map[string]any{"label": "new", "type": "ts", "validation": map[string]any{}}
	projected = projectFormJSON(response, valid, formSchemas["FormDesignRequest"])
	if reflect.DeepEqual(projected, valid) {
		t.Fatal("added question drift ignored")
	}
	userDesign, _ := decodeFormJSON(formTestDesign)
	q := userDesign.(map[string]any)["questions"].(map[string]any)["1"].(map[string]any)
	q["defaultAnswer"] = map[string]any{"users": []any{map[string]any{"id": "account-id", "name": "Display"}}}
	requestFormDesign(userDesign.(map[string]any))
	if !reflect.DeepEqual(q["defaultAnswer"], map[string]any{"users": []any{"account-id"}}) {
		t.Fatal("response user objects not normalized to request IDs")
	}
}

func TestFormTemplateOfficialExample(t *testing.T) {
	raw, err := os.ReadFile("testdata/form-template-official.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeFormJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFormSchema(v, formSchemas["FormTemplateRequest"], "template", false); err != nil {
		t.Fatal(err)
	}
}

func TestFormTemplateCreateVisibility(t *testing.T) {
	for _, mode := range []string{"eventual", "forbidden", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			client := mockFormClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					fmt.Fprintf(w, `{"id":%q}`, formTestID)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected visibility method")
				}
				if mode == "forbidden" {
					w.WriteHeader(403)
					return
				}
				if mode == "cancelled" || reads.Add(1) == 1 {
					w.WriteHeader(404)
					return
				}
				fmt.Fprintf(w, `{"id":%q,"design":%s,"publish":%s}`, formTestID, formTestDesign, formTestPublish)
			})
			r := &formTemplateResource{client: client}
			s := formTestState(t, r)
			ctx := t.Context()
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			response := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: s.Schema, Raw: s.Raw}}, &response)
			if response.Diagnostics.HasError() != (mode != "eventual") {
				t.Fatal(response.Diagnostics)
			}
			if !response.State.Raw.Equal(s.Raw) {
				t.Fatal("visibility check discarded UUID or plan")
			}
		})
	}
}
