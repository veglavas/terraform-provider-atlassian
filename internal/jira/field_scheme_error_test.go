package jira

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestFieldRequirementDelete400KeepsStateAndDiagnostic(t *testing.T) {
	client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":{"workTypeIds":"Work type is not associated with scheme"}}`))
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"startAt":0,"isLast":true,"values":[{"fieldId":"customfield_10000","parameters":{"isRequired":true}}]}`))
	})
	_, state := fieldRequirementTestState(t)
	resp := resource.DeleteResponse{State: state}
	(&fieldSchemeRequirementResource{client: client}).Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("accepted failed DELETE")
	}
	if !resp.State.Raw.Equal(state.Raw) {
		t.Fatal("failed DELETE changed state")
	}
	if !strings.Contains(resp.Diagnostics[0].Detail(), "workTypeIds") {
		t.Fatalf("lost validation detail: %v", resp.Diagnostics)
	}
}

func TestFieldRequirementDeleteValidationErrors(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"messages", `{"errorMessages":["Invalid field scheme parameters"]}`, "Invalid field scheme parameters"},
		{"field errors", `{"errors":{"workTypeIds":"Work type is not associated with scheme"}}`, "workTypeIds"},
		{"error collection", `{"errors":[{"errorMessages":["Invalid scheme ID"],"errors":{"schemeId":"Unknown scheme"}}]}`, "Invalid scheme ID"},
		{"html proxy", `<html>private proxy response</html>`, ""},
		{"plain text", `private tenant content`, ""},
		{"unrelated JSON", `{"description":"private tenant content"}`, ""},
		{"oversized", `{"errorMessages":["` + strings.Repeat("x", 20*1024) + `"]}`, ""},
		{"long message", `{"errorMessages":["Invalid: ` + strings.Repeat("x", 8000) + `"]}`, "truncated"},
		{"control characters", `{"errorMessages":["Invalid\nparameter\u001b"]}`, `Invalid\nparameter\x1b`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != fieldSchemesPath+"/fields/parameters" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			})
			err := writeFieldRequirement(context.Background(), client, fieldRequirementKey{"10000", "customfield_10000", "10001"}, nil)
			if err == nil {
				t.Fatal("accepted HTTP 400")
			}
			for _, want := range []string{"HTTP 400", "scheme_id=10000", "field_id=customfield_10000", "issue_type_id=10001", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("echoed arbitrary response content: %v", err)
			}
			if len(err.Error()) > 4096 {
				t.Fatalf("unbounded diagnostic: %d bytes", len(err.Error()))
			}
		})
	}
}
