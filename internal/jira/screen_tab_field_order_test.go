package jira

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func TestScreenTabFieldOrderIDRoundTrip(t *testing.T) {
	screenID, tabID := "10001", "20002"

	id := screenTabFieldOrderID(screenID, tabID)
	if id != "10001/20002" {
		t.Fatalf("unexpected ID %q", id)
	}

	gotScreen, gotTab, err := parseScreenTabFieldOrderID(id)
	if err != nil {
		t.Fatal(err)
	}
	if gotScreen != screenID || gotTab != tabID {
		t.Fatalf("expected %s/%s, got %s/%s", screenID, tabID, gotScreen, gotTab)
	}

	for _, invalid := range []string{"", "onlyscreen", "/20002", "10001/", "10001/20002/extra"} {
		if _, _, err := parseScreenTabFieldOrderID(invalid); err == nil {
			t.Errorf("expected error for %q", invalid)
		}
	}
}

func TestScreenTabFieldMembershipDiff(t *testing.T) {
	missing, extra := screenTabFieldMembershipDiff(
		[]string{"summary", "customfield_10001", "customfield_10002"},
		[]string{"summary", "customfield_10003", "customfield_10001"},
	)

	if len(missing) != 1 || missing[0] != "customfield_10002" {
		t.Errorf("expected missing [customfield_10002], got %v", missing)
	}
	if len(extra) != 1 || extra[0] != "customfield_10003" {
		t.Errorf("expected extra [customfield_10003], got %v", extra)
	}

	missing, extra = screenTabFieldMembershipDiff([]string{"summary"}, []string{"summary"})
	if len(missing) != 0 || len(extra) != 0 {
		t.Errorf("expected no diff, got missing=%v extra=%v", missing, extra)
	}

	missing, extra = screenTabFieldMembershipDiff(nil, nil)
	if len(missing) != 0 || len(extra) != 0 {
		t.Errorf("expected no diff for empty lists, got missing=%v extra=%v", missing, extra)
	}
}

func TestScreenTabFieldOrderEqual(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, true},
		{[]string{"a", "b", "c"}, []string{"c", "b", "a"}, false}, // order is significant
		{[]string{"a", "b"}, []string{"a", "b", "c"}, false},
		{[]string{}, []string{}, true},
		{nil, []string{}, true},
		{[]string{"a"}, nil, false},
	}

	for _, tc := range cases {
		if got := screenTabFieldOrderEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("screenTabFieldOrderEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMoveFieldRequestJSON(t *testing.T) {
	body, err := json.Marshal(moveFieldRequest{Position: "First"})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"position":"First"}` {
		t.Fatalf("unexpected body %s", body)
	}
}

func TestScreenTabFieldOrderMembershipErrorMessage(t *testing.T) {
	err := &screenTabFieldOrderMembershipError{
		ScreenID: "10001",
		TabID:    "20002",
		Missing:  []string{"customfield_10002"},
		Extra:    []string{"customfield_10003"},
	}
	msg := err.Error()
	for _, want := range []string{"customfield_10002", "customfield_10003", "atlassian_jira_screen_tab_field"} {
		if !strings.Contains(msg, want) {
			t.Errorf("membership error message %q missing %q", msg, want)
		}
	}
}

// ---------------------------------------------------------------------------
// moveAwareMock: documented screen tab fields endpoints for one screen/tab
// pair, with synchronized state and stale-read injection.
// ---------------------------------------------------------------------------

const (
	mockScreenID = "10001"
	mockTabID    = "20002"
)

type moveAwareMock struct {
	t *testing.T

	mu         sync.Mutex
	fields     []string // current live ordered field IDs
	stale      int      // GETs remaining to serve from snapshot
	snapshot   []string // pre-stale-mode live order
	gets       int
	moves      int
	moveStatus func(nth int) int // optional: HTTP status to fail the nth (1-based) move; 0 = success
	orderedIDs []string          // ordered desired list observed via moves
}

func (m *moveAwareMock) setStaleReads(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = append([]string(nil), m.fields...)
	m.stale = n
}

func (m *moveAwareMock) liveOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.fields...)
}

func (m *moveAwareMock) counters() (gets, moves int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gets, m.moves
}

func (m *moveAwareMock) replaceOrder(order []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fields = append([]string(nil), order...)
}

// fieldsPath is the documented GET path for the tab's ordered fields.
func fieldsPath() string {
	return fmt.Sprintf("/rest/api/3/screens/%s/tabs/%s/fields", mockScreenID, mockTabID)
}

func movePathFor(fieldID string) string {
	return fmt.Sprintf("/rest/api/3/screens/%s/tabs/%s/fields/%s/move", mockScreenID, mockTabID, fieldID)
}

func (m *moveAwareMock) handler() http.Handler {
	fieldsPath := fieldsPath()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			http.Error(w, "unexpected query parameters", http.StatusBadRequest)
			return
		}
		// The client must authenticate and identify itself on every request.
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "terraform-provider-atlassian/") {
			http.Error(w, fmt.Sprintf("unexpected User-Agent %q", got), http.StatusBadRequest)
			return
		}
		expectedAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-user:test-token"))
		if got := r.Header.Get("Authorization"); got != expectedAuth {
			http.Error(w, "unexpected Authorization header", http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == fieldsPath:
			m.mu.Lock()
			defer m.mu.Unlock()
			m.gets++
			serving := m.fields
			if m.stale > 0 {
				m.stale--
				serving = m.snapshot
			}
			out := make([]map[string]string, 0, len(serving))
			for _, id := range serving {
				out = append(out, map[string]string{"id": id, "name": "Field " + id})
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(out); err != nil {
				m.t.Errorf("encode fields: %v", err)
			}

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, fieldsPath+"/") && strings.HasSuffix(r.URL.Path, "/move"):
			fieldID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, fieldsPath+"/"), "/move")
			if fieldID == "" || strings.Contains(fieldID, "/") {
				http.Error(w, "bad move path", http.StatusNotFound)
				return
			}

			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// Only the documented unambiguous operation is accepted.
			if len(body) != 1 || body["position"] != "First" {
				http.Error(w, fmt.Sprintf("unexpected MoveFieldBean %v", body), http.StatusBadRequest)
				return
			}

			m.mu.Lock()
			defer m.mu.Unlock()
			m.moves++
			if m.moveStatus != nil {
				if status := m.moveStatus(m.moves); status != 0 {
					w.WriteHeader(status)
					return
				}
			}
			found := false
			next := make([]string, 0, len(m.fields))
			next = append(next, fieldID)
			for _, id := range m.fields {
				if id == fieldID {
					found = true
					continue
				}
				next = append(next, id)
			}
			if !found {
				http.Error(w, "field not on tab", http.StatusNotFound)
				return
			}
			m.fields = next
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "not found: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
}

func newMockClient(t *testing.T, mock *moveAwareMock) *atlassian.Client {
	t.Helper()
	server := httptest.NewServer(mock.handler())
	t.Cleanup(server.Close)

	client, err := atlassian.NewClient(atlassian.ClientConfig{
		URL:     server.URL,
		User:    "test-user",
		Token:   "test-token",
		Version: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestReconcileScreenTabFieldOrder_reordersShuffled(t *testing.T) {
	mock := &moveAwareMock{t: t, fields: []string{"customfield_3", "summary", "customfield_1", "customfield_2"}}
	client := newMockClient(t, mock)

	desired := []string{"summary", "customfield_1", "customfield_2", "customfield_3"}
	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired); err != nil {
		t.Fatal(err)
	}

	if got := mock.liveOrder(); !screenTabFieldOrderEqual(got, desired) {
		t.Fatalf("expected exact order %v, got %v", desired, got)
	}
	if _, moves := mock.counters(); moves != len(desired) {
		t.Fatalf("expected %d move calls, got %d", len(desired), moves)
	}
}

func TestReconcileScreenTabFieldOrder_noopWhenOrdered(t *testing.T) {
	desired := []string{"summary", "customfield_1", "customfield_2"}
	mock := &moveAwareMock{t: t, fields: append([]string(nil), desired...)}
	client := newMockClient(t, mock)

	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired); err != nil {
		t.Fatal(err)
	}

	if got := mock.liveOrder(); !screenTabFieldOrderEqual(got, desired) {
		t.Fatalf("expected order unchanged %v, got %v", desired, got)
	}
	if _, moves := mock.counters(); moves != 0 {
		t.Fatalf("expected no move calls, got %d", moves)
	}
}

func TestReconcileScreenTabFieldOrder_emptyTab(t *testing.T) {
	mock := &moveAwareMock{t: t}
	client := newMockClient(t, mock)

	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, nil); err != nil {
		t.Fatal(err)
	}
	if _, moves := mock.counters(); moves != 0 {
		t.Fatalf("expected no move calls, got %d", moves)
	}
}

func TestReconcileScreenTabFieldOrder_waitsForPlacementVisibility(t *testing.T) {
	defer shortenVerifySettings(t)()
	desired := []string{"summary", "customfield_1"}
	mock := &moveAwareMock{
		t:        t,
		fields:   desired,
		snapshot: []string{"summary"},
		stale:    2,
	}
	client := newMockClient(t, mock)
	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired); err != nil {
		t.Fatalf("expected stale placement membership to converge: %v", err)
	}
	gets, moves := mock.counters()
	if gets < 3 || moves != 0 {
		t.Fatalf("expected membership polling before any writes, got gets=%d moves=%d", gets, moves)
	}
}

func TestReconcileScreenTabFieldOrder_membershipMismatchFailsBeforeWrites(t *testing.T) {
	defer shortenVerifySettings(t)()
	for name, tc := range map[string]struct {
		fields  []string
		desired []string
	}{
		"configured field missing from tab": {
			fields:  []string{"summary", "customfield_1"},
			desired: []string{"summary", "customfield_1", "customfield_2"},
		},
		"unmanaged field on tab": {
			fields:  []string{"summary", "customfield_1", "customfield_2"},
			desired: []string{"summary", "customfield_1"},
		},
		"nonempty desired on empty tab": {
			fields:  nil,
			desired: []string{"summary"},
		},
		"empty desired on nonempty tab": {
			fields:  []string{"summary"},
			desired: nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			mock := &moveAwareMock{t: t, fields: append([]string(nil), tc.fields...)}
			client := newMockClient(t, mock)

			err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, tc.desired)
			if err == nil {
				t.Fatal("expected membership error")
			}
			var membershipErr *screenTabFieldOrderMembershipError
			if !errors.As(err, &membershipErr) {
				t.Fatalf("expected screenTabFieldOrderMembershipError, got %T: %v", err, err)
			}
			if _, moves := mock.counters(); moves != 0 {
				t.Fatalf("expected no move calls on mismatch, got %d", moves)
			}
			// Membership must be untouched.
			if got := mock.liveOrder(); !screenTabFieldOrderEqual(got, tc.fields) {
				t.Fatalf("membership changed: %v -> %v", tc.fields, got)
			}
		})
	}
}

func TestReconcileScreenTabFieldOrder_missingScreenOrTab(t *testing.T) {
	defer shortenVerifySettings(t)()
	// A server that always 404s models a missing screen or tab.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "{}", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client, err := atlassian.NewClient(atlassian.ClientConfig{
		URL:   server.URL,
		User:  "test-user",
		Token: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, []string{"summary"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error mentioning placement resources, got %v", err)
	}
}

func TestReconcileScreenTabFieldOrder_eventualConsistency(t *testing.T) {
	desired := []string{"summary", "customfield_1", "customfield_2"}
	mock := &moveAwareMock{t: t, fields: []string{"customfield_2", "customfield_1", "summary"}}
	client := newMockClient(t, mock)
	// The first two reads after the moves return the stale pre-move order.
	mock.setStaleReads(2)

	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired); err != nil {
		t.Fatal(err)
	}
	if got := mock.liveOrder(); !screenTabFieldOrderEqual(got, desired) {
		t.Fatalf("expected %v, got %v", desired, got)
	}
}

func TestReconcileScreenTabFieldOrder_verificationTimeout(t *testing.T) {
	restore := shortenVerifySettings(t)
	defer restore()

	desired := []string{"summary", "customfield_1", "customfield_2"}
	mock := &moveAwareMock{t: t, fields: []string{"customfield_2", "customfield_1", "summary"}}
	client := newMockClient(t, mock)
	// Reads stay stale forever; verification must fail bounded, not hang.
	mock.setStaleReads(1 << 30)

	err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired)
	if err == nil || !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("expected convergence timeout error, got %v", err)
	}
}

func TestReconcileScreenTabFieldOrder_partialFailureRecovers(t *testing.T) {
	desired := []string{"summary", "customfield_1", "customfield_2"}
	mock := &moveAwareMock{t: t, fields: []string{"customfield_2", "customfield_1", "summary"}}
	// Fail the second move with a non-retryable status.
	mock.moveStatus = func(nth int) int {
		if nth == 2 {
			return http.StatusForbidden
		}
		return 0
	}
	client := newMockClient(t, mock)

	err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired)
	if err == nil || !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("expected partial-failure error, got %v", err)
	}

	// Heal the remote and re-run; reconciliation is idempotent.
	mock.mu.Lock()
	mock.moveStatus = nil
	mock.mu.Unlock()

	if err := reconcileScreenTabFieldOrder(t.Context(), client, mockScreenID, mockTabID, desired); err != nil {
		t.Fatal(err)
	}
	if got := mock.liveOrder(); !screenTabFieldOrderEqual(got, desired) {
		t.Fatalf("expected %v, got %v", desired, got)
	}
}

func TestGetScreenTabFieldIDs_rejectsMalformedRemote(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate ids": `[{"id":"summary","name":"Summary"},{"id":"summary","name":"Summary"}]`,
		"empty id":      `[{"id":"","name":"Nameless"}]`,
		"null array":    `null`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != fieldsPath() || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)

			client, err := atlassian.NewClient(atlassian.ClientConfig{
				URL:   server.URL,
				User:  "test-user",
				Token: "test-token",
			})
			if err != nil {
				t.Fatal(err)
			}

			_, _, err = getScreenTabFieldIDs(t.Context(), client, mockScreenID, mockTabID)
			if err == nil {
				t.Fatal("expected malformed remote order to be rejected")
			}
		})
	}
}

func shortenVerifySettings(t *testing.T) func() {
	t.Helper()
	return SetScreenTabFieldOrderVerifySettings(500*time.Millisecond, 20*time.Millisecond)
}
