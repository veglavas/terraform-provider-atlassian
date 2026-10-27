package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func fieldSchemeTestClient(t *testing.T, handler http.HandlerFunc) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, token, ok := r.BasicAuth()
		if !ok || user != "test@example.org" || token != "test" {
			t.Error("missing test authentication")
		}
		if r.Header.Get("User-Agent") != "terraform-provider-atlassian/test" || r.Header.Get("X-ExperimentalApi") != "opt-in" {
			t.Error("missing standard/experimental header")
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("missing Accept header")
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := atlassian.NewClient(atlassian.ClientConfig{URL: server.URL, User: "test@example.org", Token: "test", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestFieldRequirementRead(t *testing.T) {
	key := fieldRequirementKey{"10000", "customfield_10000", "10001"}
	for _, tc := range []struct {
		name, fields               string
		status                     int
		required, found, wantError bool
	}{
		{"override", `[{"fieldId":"customfield_10000","parameters":{"isRequired":true},"workTypeParameters":[{"workTypeId":"10001","isRequired":false}]}]`, 200, false, true, false},
		{"inherited", `[{"fieldId":"customfield_10000","parameters":{"isRequired":true}}]`, 200, true, true, false},
		{"description-only override", `[{"fieldId":"customfield_10000","parameters":{"isRequired":false},"workTypeParameters":[{"workTypeId":"10001","description":"preserved"}]}]`, 200, false, true, false},
		{"restricted", `[{"fieldId":"customfield_10000","restrictedToWorkTypes":["10002"],"parameters":{"isRequired":true}}]`, 200, false, false, false},
		{"missing association", `[]`, 200, false, false, false},
		{"missing parent", `[]`, 404, false, false, false},
		{"permission", `[]`, 403, false, false, true},
		{"wrong field", `[{"fieldId":"summary","parameters":{"isRequired":true}}]`, 200, false, false, true},
		{"missing field ID", `[{}]`, 200, false, false, true},
		{"duplicate field", `[{"fieldId":"customfield_10000"},{"fieldId":"customfield_10000"}]`, 200, false, false, true},
		{"duplicate type", `[{"fieldId":"customfield_10000","parameters":{"isRequired":true},"workTypeParameters":[{"workTypeId":"10001","isRequired":true},{"workTypeId":"10001","isRequired":false}]}]`, 200, false, false, true},
		{"malformed", `not json`, 200, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status == http.StatusNotFound && r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if r.Method != "GET" || r.URL.Path != fieldSchemesPath+"/10000/fields" || r.URL.RawQuery != "fieldId=customfield_10000&startAt=0&maxResults=100" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"startAt":0,"total":%d,"isLast":true,"values":%s}`, strings.Count(tc.fields, `"fieldId"`), tc.fields)
			})
			required, found, err := readFieldRequirement(context.Background(), client, key)
			if (err != nil) != tc.wantError || required != tc.required || found != tc.found {
				t.Fatalf("required=%v found=%v err=%v", required, found, err)
			}
		})
	}
}

func TestFieldSchemeStrictPagination(t *testing.T) {
	for _, tc := range []struct{ name, page string }{
		{"missing values", `{"startAt":0,"isLast":true}`},
		{"null values", `{"startAt":0,"isLast":true,"values":null}`},
		{"missing offset", `{"isLast":true,"values":[]}`},
		{"missing completion", `{"startAt":0,"values":[]}`},
		{"wrong offset", `{"startAt":1,"isLast":true,"values":[]}`},
		{"empty nonfinal", `{"startAt":0,"isLast":false,"values":[]}`},
		{"inconsistent total", `{"startAt":0,"isLast":true,"total":2,"values":[]}`},
		{"contradictory completion", `{"startAt":0,"isLast":false,"total":0,"values":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.RawQuery != "startAt=0&maxResults=100" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
				}
				_, _ = w.Write([]byte(tc.page))
			})
			values, missing, err := fieldSchemePages[json.RawMessage](context.Background(), client, fieldSchemesPath)
			if err == nil || missing || values != nil {
				t.Fatalf("accepted partial collection %v %v %v", values, missing, err)
			}
		})
	}
	t.Run("server capped pages", func(t *testing.T) {
		client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.RawQuery {
			case "startAt=0&maxResults=100":
				_, _ = w.Write([]byte(`{"startAt":0,"total":2,"isLast":false,"values":[1]}`))
			case "startAt=1&maxResults=100":
				_, _ = w.Write([]byte(`{"startAt":1,"total":2,"isLast":true,"values":[2]}`))
			default:
				t.Errorf("unexpected query %s", r.URL)
				w.WriteHeader(400)
			}
		})
		values, missing, err := fieldSchemePages[int](context.Background(), client, fieldSchemesPath)
		if err != nil || missing || !reflect.DeepEqual(values, []int{1, 2}) {
			t.Fatalf("%v %v %v", values, missing, err)
		}
	})
	t.Run("repeated page", func(t *testing.T) {
		client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"startAt":0,"total":2,"isLast":false,"values":[1]}`))
		})
		values, _, err := fieldSchemePages[int](context.Background(), client, fieldSchemesPath)
		if err == nil || values != nil {
			t.Fatal("accepted repeated page")
		}
	})
}

func TestFieldRequirementWriteResults(t *testing.T) {
	required := false
	key := fieldRequirementKey{"10000", "customfield_10000", "10001"}
	for _, tc := range []struct {
		name, body        string
		status            int
		remove, wantError bool
	}{
		{"put success", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeId":10001,"success":true}]}`, 200, false, false},
		{"put scheme result", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"success":true}]}`, 200, false, false},
		{"put no content", "", 204, false, false},
		{"partial failure", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeId":10001,"success":false,"error":"private tenant content"}]}`, 207, false, true},
		{"failure in 200", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"success":false}]}`, 200, false, true},
		{"missing success", `{"results":[{"fieldId":"customfield_10000","schemeId":10000}]}`, 200, false, true},
		{"wrong scheme", `{"results":[{"fieldId":"customfield_10000","schemeId":10002,"success":true}]}`, 200, false, true},
		{"wrong work type", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeId":10003,"success":true}]}`, 200, false, true},
		{"missing result", `{"results":[]}`, 200, false, true},
		{"empty put", "", 200, false, true},
		{"malformed", "invalid", 200, false, true},
		{"forbidden", "private tenant content", 403, false, true},
		{"delete success", "", 200, true, false},
		{"delete empty object", `{}`, 200, true, false},
		{"delete null", `null`, 200, true, false},
		{"delete empty results", `{"results":[]}`, 200, true, false},
		{"delete no content", "", 204, true, false},
		{"delete 207 success", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeIds":[10001],"success":true}]}`, 207, true, false},
		{"delete 207 error", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeIds":[10001],"success":false,"error":{"code":"FIELD_IS_NOT_ASSOCIATED","message":"private tenant content"}}]}`, 207, true, true},
		{"delete wrong types", `{"results":[{"fieldId":"customfield_10000","schemeId":10000,"workTypeIds":[10002],"success":true}]}`, 207, true, true},
		{"delete empty 207", "", 207, true, true},
		{"bulk endpoint 404", "", 404, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				method := "PUT"
				want := `{"customfield_10000":[{"schemeIds":[10000],"workTypeParameters":[{"workTypeId":10001,"isRequired":false}]}]}`
				if tc.remove {
					method = "DELETE"
					want = `{"customfield_10000":[{"schemeId":10000,"workTypeIds":[10001],"parameters":["isRequired"]}]}`
				}
				if r.Method != method || r.URL.Path != fieldSchemesPath+"/fields/parameters" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected write %s %s", r.Method, r.URL)
				}
				var body, expected any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if err := json.Unmarshal([]byte(want), &expected); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if !reflect.DeepEqual(body, expected) {
					t.Errorf("body=%v want=%v", body, expected)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			var value *bool
			if !tc.remove {
				value = &required
			}
			err := writeFieldRequirement(context.Background(), client, key, value)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private tenant content") {
				t.Fatal("echoed arbitrary response content")
			}
		})
	}
}

func TestProjectFieldSchemeLookup(t *testing.T) {
	for _, tc := range []struct {
		name, mappings, scheme string
		wantError              bool
	}{
		{"found", `[{"projectId":10001,"schemeId":10000}]`, `{"id":"10000","name":"Shared","description":"Managed"}`, false},
		{"missing", `[]`, `{}`, true},
		{"duplicate", `[{"projectId":10001,"schemeId":10000},{"projectId":10001,"schemeId":10000}]`, `{}`, true},
		{"wrong project", `[{"projectId":10002,"schemeId":10000}]`, `{}`, true},
		{"invalid ID", `[{"projectId":10001,"schemeId":0}]`, `{}`, true},
		{"wrong scheme", `[{"projectId":10001,"schemeId":10000}]`, `{"id":"10002","name":"Wrong"}`, true},
		{"partial scheme", `[{"projectId":10001,"schemeId":10000}]`, `{"id":"10000"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := fieldSchemeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/projects" && r.URL.RawQuery == "projectId=10001&startAt=0&maxResults=100":
					_, _ = fmt.Fprintf(w, `{"startAt":0,"isLast":true,"values":%s}`, tc.mappings)
				case r.Method == "GET" && r.URL.Path == fieldSchemesPath+"/10000" && r.URL.RawQuery == "":
					_, _ = w.Write([]byte(tc.scheme))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
				}
			})
			result, err := readProjectFieldScheme(context.Background(), client, "10001")
			if (err != nil) != tc.wantError {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestFieldSchemeCanonicalIDs(t *testing.T) {
	for _, id := range []string{"", "0", "-1", "01", " 1", "+1", "abc", "9223372036854775808"} {
		if _, err := positiveID(id); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	if _, err := positiveID("9223372036854775807"); err != nil {
		t.Fatal(err)
	}
}
