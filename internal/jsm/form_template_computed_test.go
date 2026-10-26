package jsm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFormTemplateCreateReadsComputedPublishing(t *testing.T) {
	for _, scenario := range []string{"null", "unknown", "configured"} {
		t.Run(scenario, func(t *testing.T) {
			var creates atomic.Int32
			client := mockFormClient(t, func(w http.ResponseWriter, req *http.Request) {
				switch req.Method {
				case http.MethodPost:
					creates.Add(1)
					var body map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if _, ok := body["publish"]; ok && scenario != "configured" {
						t.Error("unconfigured publishing sent in create request")
					}
					fmt.Fprintf(w, `{"id":%q}`, formTestID)
				case http.MethodGet:
					// The readiness GET deliberately diverges from the configured
					// publication so preserving the plan value is observable.
					fmt.Fprintf(w, `{"id":%q,"design":%s,"publish":{"portal":{"portalRequestTypeIds":[99],"submitOnCreate":false,"validateOnCreate":false}}}`, formTestID, formTestDesign)
				default:
					t.Errorf("unexpected method %s", req.Method)
					w.WriteHeader(http.StatusNotFound)
				}
			})
			r := &formTemplateResource{client: client}
			var schema resource.SchemaResponse
			r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
			model := formTestModel()
			model.ID = types.StringUnknown()
			model.Publish = formJSON{types.StringNull()}
			if scenario == "unknown" {
				model.Publish = formJSON{types.StringUnknown()}
			}
			wantPublish := `{"portal":{"portalRequestTypeIds":[99],"submitOnCreate":false,"validateOnCreate":false}}`
			if scenario == "configured" {
				model.Publish = formJSON{types.StringValue(formTestPublish)}
				wantPublish = formTestPublish
			}
			plan := tfsdk.Plan{Schema: schema.Schema}
			if diags := plan.Set(t.Context(), &model); diags.HasError() {
				t.Fatal(diags)
			}
			response := resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema}}
			r.Create(t.Context(), resource.CreateRequest{Plan: plan}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var got formTemplateModel
			if diags := response.State.Get(t.Context(), &got); diags.HasError() {
				t.Fatal(diags)
			}
			equal, diags := got.Publish.StringSemanticEquals(t.Context(), formJSON{types.StringValue(wantPublish)})
			if diags.HasError() || !equal {
				t.Fatalf("publish mismatch (scenario %s): got %s want %s", scenario, got.Publish.ValueString(), wantPublish)
			}
			if got.Design.ValueString() != formTestDesign || creates.Load() != 1 {
				t.Fatal("configured design changed or create repeated")
			}
		})
	}
}
