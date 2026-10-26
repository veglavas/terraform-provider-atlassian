package jsm_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

type formProtocolTransport func(*http.Request) (*http.Response, error)

func (f formProtocolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Complements the Terraform CLI lifecycle test: exercises the real framework
// protocol and private state in-process, including in socket-restricted sandboxes.
func TestFormTemplateProtocolLifecycle(t *testing.T) {
	var mu sync.Mutex
	var remote map[string]any
	var posts, puts, deletes int
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: "https://forms-test.atlassian.net", User: "test", Token: "test", Version: "test", HTTPClient: &http.Client{Transport: formProtocolTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		w := httptest.NewRecorder()
		if r.URL.RawQuery != "" {
			t.Error("unexpected query")
		}
		if r.URL.Host == "forms-test.atlassian.net" && r.Method == "GET" && r.URL.Path == "/_edge/tenant_info" {
			fmt.Fprintf(w, `{"cloudId":%q}`, testutil.FormsTestCloudID)
			return w.Result(), nil
		}
		if r.URL.Host != "api.atlassian.com" || !strings.HasPrefix(r.URL.Path, testutil.FormsTestPrefix+"/project/JML/form") {
			return nil, fmt.Errorf("unexpected mock destination")
		}
		switch r.Method {
		case "POST", "PUT":
			if r.Method == "POST" {
				posts++
			} else {
				puts++
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			remote = body
			remote["id"] = lifecycleFormID
			fmt.Fprintf(w, `{"id":%q}`, lifecycleFormID)
		case "GET":
			if remote == nil {
				w.WriteHeader(404)
			} else {
				// API optional metadata must not change the configured design.
				remote["design"].(map[string]any)["settings"].(map[string]any)["language"] = "en"
				_ = json.NewEncoder(w).Encode(remote)
			}
		case "DELETE":
			deletes++
			remote = nil
			w.WriteHeader(404)
		default:
			t.Error("unexpected method")
			w.WriteHeader(405)
		}
		return w.Result(), nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := testutil.ProtoV6ProviderFactoriesWithClient(client)["atlassian"]()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(diags []*tfprotov6.Diagnostic) {
		t.Helper()
		for _, d := range diags {
			if d.Severity == tfprotov6.DiagnosticSeverityError {
				t.Fatalf("%s: %s", d.Summary, d.Detail)
			}
		}
	}
	check(schemas.Diagnostics)
	dynamic := func(typ tftypes.Type, value any) *tfprotov6.DynamicValue {
		t.Helper()
		v, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, value))
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	providerValues := map[string]tftypes.Value{}
	for _, a := range schemas.Provider.Block.Attributes {
		providerValues[a.Name] = tftypes.NewValue(a.ValueType(), nil)
	}
	configured, err := server.ConfigureProvider(t.Context(), &tfprotov6.ConfigureProviderRequest{TerraformVersion: "1.13.0", Config: dynamic(schemas.Provider.ValueType(), providerValues)})
	if err != nil {
		t.Fatal(err)
	}
	check(configured.Diagnostics)
	name := "atlassian_jsm_form_template"
	typ := schemas.ResourceSchemas[name].ValueType()
	design := `{"conditions":{},"layout":[{}],"questions":{"1":{"type":"ts","label":"Employee","jiraField":"customfield_10000","validation":{}}},"sections":{},"settings":{"name":"Joiner","submit":{"lock":true,"pdf":false}}}`
	publishing := `{"portal":{"portalRequestTypeIds":[63],"submitOnCreate":true,"validateOnCreate":true}}`
	configValue := func(project, design, publish, id any) *tfprotov6.DynamicValue {
		return dynamic(typ, map[string]tftypes.Value{"project_id": tftypes.NewValue(tftypes.String, project), "design": tftypes.NewValue(tftypes.String, design), "publish": tftypes.NewValue(tftypes.String, publish), "id": tftypes.NewValue(tftypes.String, id)})
	}
	unknown := configValue(tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue, nil)
	validation, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: name, Config: unknown})
	if err != nil {
		t.Fatal(err)
	}
	check(validation.Diagnostics)
	for _, bad := range []struct{ design, publish string }{{`[]`, publishing}, {design, `{"portal":{"portalRequestTypeIds":["63"]}}`}, {`{}`, publishing}} {
		v, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: name, Config: configValue("JML", bad.design, bad.publish, nil)})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, d := range v.Diagnostics {
			found = found || d.Severity == tfprotov6.DiagnosticSeverityError
		}
		if !found {
			t.Fatal("invalid JSON schema accepted")
		}
	}
	config := configValue("JML", design, publishing, nil)
	valid, err := server.ValidateResourceConfig(t.Context(), &tfprotov6.ValidateResourceConfigRequest{TypeName: name, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	check(valid.Diagnostics)
	null := dynamic(typ, nil)
	plan, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{TypeName: name, Config: config, PriorState: null, ProposedNewState: config})
	if err != nil {
		t.Fatal(err)
	}
	check(plan.Diagnostics)
	applied, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: name, Config: config, PriorState: null, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	check(applied.Diagnostics)
	if len(applied.Private) == 0 {
		t.Fatal("configuration shape not persisted")
	}
	refreshed, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: applied.NewState, Private: applied.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(refreshed.Diagnostics)
	before, _ := applied.NewState.Unmarshal(typ)
	after, _ := refreshed.NewState.Unmarshal(typ)
	if !before.Equal(after) {
		t.Fatal("optional defaults or normalization created drift")
	}
	// Formatting changes in the API response must preserve configured values.
	attrs := map[string]tftypes.Value{}
	if err := after.As(&attrs); err != nil {
		t.Fatal(err)
	}
	var actualDesign string
	if err := attrs["design"].As(&actualDesign); err != nil {
		t.Fatal(err)
	}
	if actualDesign != design {
		t.Fatal("semantic equality failed to preserve configured JSON")
	}
	dsType := schemas.DataSourceSchemas[name].ValueType()
	dsConfig := dynamic(dsType, map[string]tftypes.Value{"project_id": tftypes.NewValue(tftypes.String, "JML"), "id": tftypes.NewValue(tftypes.String, lifecycleFormID), "design": tftypes.NewValue(tftypes.String, nil), "publish": tftypes.NewValue(tftypes.String, nil)})
	ds, err := server.ReadDataSource(t.Context(), &tfprotov6.ReadDataSourceRequest{TypeName: name, Config: dsConfig})
	if err != nil {
		t.Fatal(err)
	}
	check(ds.Diagnostics)
	imported, err := server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: name, ID: "JML/" + lifecycleFormID})
	if err != nil {
		t.Fatal(err)
	}
	check(imported.Diagnostics)
	if len(imported.ImportedResources) != 1 {
		t.Fatal("import failed")
	}
	ir, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: imported.ImportedResources[0].State})
	if err != nil {
		t.Fatal(err)
	}
	check(ir.Diagnostics)
	// Change publishing and design in place, using retained private state.
	updatedDesign := strings.Replace(design, "Joiner", "Mover", 1)
	updatedPublishing := strings.Replace(publishing, "[63]", "[]", 1)
	nextConfig := configValue("JML", updatedDesign, updatedPublishing, nil)
	nextProposed := configValue("JML", updatedDesign, updatedPublishing, lifecycleFormID)
	nextPlan, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{TypeName: name, Config: nextConfig, PriorState: refreshed.NewState, ProposedNewState: nextProposed, PriorPrivate: refreshed.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(nextPlan.Diagnostics)
	updated, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: name, Config: nextConfig, PriorState: refreshed.NewState, PlannedState: nextPlan.PlannedState, PlannedPrivate: nextPlan.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	check(updated.Diagnostics)
	updatedRead, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: updated.NewState, Private: updated.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(updatedRead.Diagnostics)
	ua, _ := updated.NewState.Unmarshal(typ)
	ur, _ := updatedRead.NewState.Unmarshal(typ)
	if !ua.Equal(ur) {
		t.Fatal("updated configuration shape not persisted")
	}

	// Removing publish from configuration must preserve the last observed settings
	// through a design update, and switch subsequent reads to full observation.
	omittedDesign := strings.Replace(updatedDesign, "Mover", "Leaver", 1)
	omittedConfig := configValue("JML", omittedDesign, nil, nil)
	omittedPlan, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{TypeName: name, Config: omittedConfig, PriorState: updatedRead.NewState, ProposedNewState: configValue("JML", omittedDesign, nil, lifecycleFormID), PriorPrivate: updatedRead.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(omittedPlan.Diagnostics)
	omitted, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: name, Config: omittedConfig, PriorState: updatedRead.NewState, PlannedState: omittedPlan.PlannedState, PlannedPrivate: omittedPlan.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	check(omitted.Diagnostics)
	mu.Lock()
	_, hasPublishing := remote["publish"]
	mu.Unlock()
	if !hasPublishing {
		t.Fatal("omitting publish discarded the observed publishing object")
	}
	updated = omitted
	ua, _ = updated.NewState.Unmarshal(typ)
	// Real configured drift must still appear in state.
	mu.Lock()
	remote["design"].(map[string]any)["questions"].(map[string]any)["1"].(map[string]any)["label"] = "Drift"
	mu.Unlock()
	drift, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: updated.NewState, Private: updated.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(drift.Diagnostics)
	driftState, _ := drift.NewState.Unmarshal(typ)
	if driftState.Equal(ua) {
		t.Fatal("real drift ignored")
	}
	deleted, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{TypeName: name, Config: null, PriorState: updated.NewState, PlannedState: null, PlannedPrivate: updated.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(deleted.Diagnostics)
	deletedValue, _ := deleted.NewState.Unmarshal(typ)
	if !deletedValue.IsNull() {
		t.Fatal("delete 404 did not remove resource")
	}
	missing, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: updated.NewState, Private: updated.Private})
	if err != nil {
		t.Fatal(err)
	}
	check(missing.Diagnostics)
	missingValue, _ := missing.NewState.Unmarshal(typ)
	if !missingValue.IsNull() {
		t.Fatal("missing read did not remove state")
	}
	if posts != 1 || puts != 2 || deletes != 1 {
		t.Fatalf("incorrect write counts: %d/%d/%d", posts, puts, deletes)
	}
}
