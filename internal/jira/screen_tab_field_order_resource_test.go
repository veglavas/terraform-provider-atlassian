package jira_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/jira"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/testutil"
)

// ---------------------------------------------------------------------------
// screenTabFieldOrderServer: mock Jira serving the documented screen,
// screen tab, screen tab field, and screen tab field move endpoints.
// ---------------------------------------------------------------------------

const (
	orderTestScreenID = "10001"
	orderTestTabID    = "20002"
	orderTestKey      = orderTestScreenID + "/" + orderTestTabID
)

type screenTabFieldOrderServer struct {
	t *testing.T

	mu      sync.Mutex
	screens []map[string]interface{}
	tabs    map[string][]map[string]interface{} // screenID → []tab
	fields  map[string][]string                 // "screenID/tabID" → ordered field IDs

	// Injected behavior for the default tab only.
	getStatus  func(gets int) int    // optional: HTTP status to fail GETs
	getRawBody func(gets int) string // optional: raw JSON body for GETs (malformed remote)
	stale      int                   // remaining stale GETs served from snapshot
	snapshot   []string              // stale snapshot

	// seedTabFields runs after a tab is created (used to place initial fields).
	seedTabFields func(screenID, tabID string)

	gets, moves, adds, removals int
	moveStatus                  func(nth int) int // optional: status to fail the nth (1-based) move

	nextScreenID atomic.Int64
	nextTabID    atomic.Int64
}

func newScreenTabFieldOrderServer(t *testing.T, initial []string) *screenTabFieldOrderServer {
	t.Helper()
	s := &screenTabFieldOrderServer{
		t:            t,
		screens:      []map[string]interface{}{},
		tabs:         map[string][]map[string]interface{}{},
		fields:       map[string][]string{},
		nextScreenID: atomic.Int64{},
		nextTabID:    atomic.Int64{},
	}
	s.nextScreenID.Store(50000)
	s.nextTabID.Store(60000)

	// The default screen/tab pair always exists.
	s.screens = append(s.screens, screenJSON(10001, "Order Test Screen", ""))
	s.tabs[orderTestScreenID] = []map[string]interface{}{tabJSON(20002, "Order Test Tab")}
	s.fields[orderTestKey] = append([]string(nil), initial...)

	return s
}

func (s *screenTabFieldOrderServer) start() string {
	server := httptest.NewServer(http.HandlerFunc(s.handler))
	s.t.Cleanup(server.Close)
	return server.URL
}

func (s *screenTabFieldOrderServer) counters() (gets, moves, adds, removals int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets, s.moves, s.adds, s.removals
}

// replaceOrder rewrites the remote order of the default tab out-of-band.
func (s *screenTabFieldOrderServer) replaceOrder(order []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fields[orderTestKey] = append([]string(nil), order...)
}

// removeDefaultTab simulates an out-of-band deletion of the tab.
func (s *screenTabFieldOrderServer) removeDefaultTab() {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tabs, orderTestScreenID)
	delete(s.fields, orderTestKey)
}

// liveOrder returns the current ordered field IDs of the default tab.
func (s *screenTabFieldOrderServer) liveOrder() []string {
	return s.liveOrderFor(orderTestScreenID, orderTestTabID)
}

// liveOrderFor returns the current ordered field IDs of any tab.
func (s *screenTabFieldOrderServer) liveOrderFor(screenID, tabID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.fields[screenID+"/"+tabID]...)
}

func (s *screenTabFieldOrderServer) setStaleReads(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = append([]string(nil), s.fields[orderTestKey]...)
	s.stale = n
}

func (s *screenTabFieldOrderServer) fieldsBody(ids []string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		out = append(out, fieldJSON(id, "Field "+id))
	}
	return out
}

func (s *screenTabFieldOrderServer) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.t.Errorf("encode response: %v", err)
	}
}

func (s *screenTabFieldOrderServer) handler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/screens" {
		query := r.URL.Query()
		if len(query) != 2 || query.Get("startAt") != "0" || query.Get("maxResults") != "100" {
			http.Error(w, "unexpected screen pagination", http.StatusBadRequest)
			return
		}
	} else if r.URL.RawQuery != "" {
		http.Error(w, "unexpected query parameters", http.StatusBadRequest)
		return
	}
	// Every provider request must authenticate and identify itself.
	if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "terraform-provider-atlassian/") {
		http.Error(w, fmt.Sprintf("unexpected User-Agent %q", ua), http.StatusBadRequest)
		return
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("test-user:test-token"))
	if auth := r.Header.Get("Authorization"); auth != wantAuth {
		http.Error(w, "unexpected Authorization header", http.StatusUnauthorized)
		return
	}

	switch {
	// ---- Screens list/create
	case r.Method == "POST" && r.URL.Path == "/rest/api/3/screens":
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id := s.nextScreenID.Add(1)
		screen := screenJSON(id, body["name"], body["description"])
		s.mu.Lock()
		s.screens = append(s.screens, screen)
		s.mu.Unlock()
		s.writeJSON(w, http.StatusCreated, screen)

	case r.Method == "GET" && r.URL.Path == "/rest/api/3/screens":
		s.mu.Lock()
		all := make([]map[string]interface{}, len(s.screens))
		copy(all, s.screens)
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"values": all, "isLast": true, "startAt": 0, "maxResults": 100, "total": len(all),
		})

	case r.Method == "DELETE" && isScreenPath(r.URL.Path) && !isTabPath(r.URL.Path) && !isFieldPath(r.URL.Path):
		screenID := extractScreenID(r.URL.Path)
		s.mu.Lock()
		kept := s.screens[:0]
		for _, sc := range s.screens {
			if fmt.Sprintf("%v", sc["id"]) != screenID {
				kept = append(kept, sc)
			}
		}
		s.screens = kept
		delete(s.tabs, screenID)
		for key := range s.fields {
			if strings.HasPrefix(key, screenID+"/") {
				delete(s.fields, key)
			}
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	// ---- Tabs list/create
	case r.Method == "POST" && isTabsListPath(r.URL.Path):
		screenID := extractScreenID(r.URL.Path)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id := s.nextTabID.Add(1)
		tab := tabJSON(id, body["name"])
		s.mu.Lock()
		s.tabs[screenID] = append(s.tabs[screenID], tab)
		if s.seedTabFields != nil {
			s.seedTabFields(screenID, fmt.Sprintf("%d", id))
		}
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, tab)

	case r.Method == "GET" && isTabsListPath(r.URL.Path):
		screenID := extractScreenID(r.URL.Path)
		s.mu.Lock()
		tabs := s.tabs[screenID]
		all := make([]map[string]interface{}, len(tabs))
		copy(all, tabs)
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, all)

	case r.Method == "DELETE" && isTabPath(r.URL.Path) && !isFieldPath(r.URL.Path):
		screenID, tabID := extractScreenAndTabID(r.URL.Path)
		s.mu.Lock()
		kept := s.tabs[screenID][:0]
		for _, tab := range s.tabs[screenID] {
			if fmt.Sprintf("%v", tab["id"]) != tabID {
				kept = append(kept, tab)
			}
		}
		s.tabs[screenID] = kept
		delete(s.fields, screenID+"/"+tabID)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	// ---- Tab fields list (documented ordered GET)
	case r.Method == "GET" && isFieldsListPath(r.URL.Path):
		screenID, tabID := extractScreenAndTabIDFromFields(r.URL.Path)
		key := screenID + "/" + tabID

		s.mu.Lock()
		s.gets++
		gets := s.gets
		if s.getStatus != nil {
			if status := s.getStatus(gets); status != 0 {
				s.mu.Unlock()
				w.WriteHeader(status)
				return
			}
		}
		if s.getRawBody != nil {
			if body := s.getRawBody(gets); body != "" {
				s.mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body))
				return
			}
		}
		if _, ok := s.tabs[screenID]; !ok {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
		foundTab := false
		for _, tab := range s.tabs[screenID] {
			if fmt.Sprintf("%v", tab["id"]) == tabID {
				foundTab = true
				break
			}
		}
		if !foundTab {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
		order := s.fields[key]
		if s.stale > 0 {
			s.stale--
			order = s.snapshot
		}
		body := s.fieldsBody(order)
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, body)

	// ---- Add field to tab (placement)
	case r.Method == "POST" && isFieldsListPath(r.URL.Path):
		screenID, tabID := extractScreenAndTabIDFromFields(r.URL.Path)
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		fieldID := body["fieldId"]
		if len(body) != 1 || fieldID == "" {
			http.Error(w, "fieldId is required", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		key := screenID + "/" + tabID
		s.fields[key] = append(s.fields[key], fieldID)
		s.adds++
		s.mu.Unlock()
		s.writeJSON(w, http.StatusOK, fieldJSON(fieldID, "Field "+fieldID))

	// ---- Remove field from tab (placement)
	case r.Method == "DELETE" && isFieldPath(r.URL.Path):
		screenID, tabID, fieldID := extractScreenTabFieldID(r.URL.Path)
		s.mu.Lock()
		key := screenID + "/" + tabID
		kept := s.fields[key][:0]
		for _, id := range s.fields[key] {
			if id != fieldID {
				kept = append(kept, id)
			}
		}
		s.fields[key] = kept
		s.removals++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	// ---- Move field (documented MoveFieldBean)
	case r.Method == "POST" && isMovePath(r.URL.Path):
		screenID, tabID, fieldID := extractMoveIDs(r.URL.Path)
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(body) != 1 || body["position"] != "First" {
			http.Error(w, fmt.Sprintf("unexpected MoveFieldBean %v", body), http.StatusBadRequest)
			return
		}

		s.mu.Lock()
		key := screenID + "/" + tabID
		s.moves++
		if s.moveStatus != nil {
			if status := s.moveStatus(s.moves); status != 0 {
				s.mu.Unlock()
				w.WriteHeader(status)
				return
			}
		}
		found := false
		next := make([]string, 0, len(s.fields[key]))
		next = append(next, fieldID)
		for _, id := range s.fields[key] {
			if id == fieldID {
				found = true
				continue
			}
			next = append(next, id)
		}
		if !found {
			s.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
		s.fields[key] = next
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, "not found: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
	}
}

// isMovePath matches /rest/api/3/screens/{screenId}/tabs/{tabId}/fields/{fieldId}/move
func isMovePath(p string) bool {
	parts := strings.Split(strings.TrimPrefix(p, "/rest/api/3/screens/"), "/")
	return len(parts) == 6 && parts[1] == "tabs" && parts[3] == "fields" && parts[4] != "" && parts[5] == "move"
}

func extractMoveIDs(p string) (string, string, string) {
	parts := strings.Split(strings.TrimPrefix(p, "/rest/api/3/screens/"), "/")
	return parts[0], parts[2], parts[4]
}

func setupScreenTabFieldOrderEnv(t *testing.T, url string) {
	t.Helper()
	t.Setenv("ATLASSIAN_URL", url)
	t.Setenv("ATLASSIAN_USER", "test-user")
	t.Setenv("ATLASSIAN_TOKEN", "test-token")
}

// ---------------------------------------------------------------------------
// Lifecycle tests
// ---------------------------------------------------------------------------

func TestAccScreenTabFieldOrderResource_basic(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"customfield_2", "customfield_1", "summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	movesAtLastCheck := 0

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Destroy must relinquish ownership without any remote mutation.
			_, moves, adds, removals := mock.counters()
			if moves != movesAtLastCheck {
				return fmt.Errorf("destroy must not reorder fields: moves changed from %d to %d", movesAtLastCheck, moves)
			}
			if adds != 0 || removals != 0 {
				return fmt.Errorf("ordering must never change membership: adds=%d removals=%d", adds, removals)
			}
			got := mock.liveOrder()
			want := []string{"customfield_1", "summary", "customfield_2"}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				return fmt.Errorf("remote order must be preserved after destroy: got %v", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// Create from a shuffled order; moves produce the exact plan.
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "id", orderTestKey),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.#", "3"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "summary"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.1", "customfield_1"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.2", "customfield_2"),
				),
			},
			// Already ordered: the second plan must be empty (no moves).
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				Check: func(*terraform.State) error {
					_, moves, _, _ := mock.counters()
					if moves != 3 {
						return fmt.Errorf("expected 3 moves from create only, got %d", moves)
					}
					movesAtLastCheck = moves
					return nil
				},
			},
			// Update to a different order in place; list order is significant.
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["customfield_1", "summary", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "customfield_1"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.1", "summary"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.2", "customfield_2"),
					func(*terraform.State) error {
						if got := mock.liveOrder(); strings.Join(got, ",") != "customfield_1,summary,customfield_2" {
							return fmt.Errorf("expected remote order customfield_1,summary,customfield_2, got %v", got)
						}
						_, moves, _, _ := mock.counters()
						if moves != 6 {
							return fmt.Errorf("expected 6 total moves after update, got %d", moves)
						}
						movesAtLastCheck = moves
						return nil
					},
				),
			},
			// Out-of-band reordering is detected at the next plan and corrected
			// in place by the apply. ExpectNonEmptyPlan is deliberately not set:
			// it also asserts on the post-apply refresh plan, which must be empty
			// once the drift is corrected.
			{
				PreConfig: func() {
					mock.replaceOrder([]string{"customfield_2", "customfield_1", "summary"})
				},
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["customfield_1", "summary", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "customfield_1"),
					func(*terraform.State) error {
						if got := mock.liveOrder(); strings.Join(got, ",") != "customfield_1,summary,customfield_2" {
							return fmt.Errorf("expected drift corrected to customfield_1,summary,customfield_2, got %v", got)
						}
						_, moves, _, _ := mock.counters()
						if moves != 9 {
							return fmt.Errorf("expected 9 total moves after drift correction, got %d", moves)
						}
						movesAtLastCheck = moves
						return nil
					},
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_alreadyOrderedNoop(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary", "customfield_1"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	config := fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1"]
}`, orderTestScreenID, orderTestTabID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			_, moves, adds, removals := mock.counters()
			if moves != 0 || adds != 0 || removals != 0 {
				return fmt.Errorf("expected zero writes, got moves=%d adds=%d removals=%d", moves, adds, removals)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config},
			// Second identical apply must produce an empty plan without writes.
			{Config: config},
		},
	})
}

func TestAccScreenTabFieldOrderResource_emptyTab(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	config := fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = []
}`, orderTestScreenID, orderTestTabID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			_, moves, _, _ := mock.counters()
			if moves != 0 {
				return fmt.Errorf("expected zero moves, got %d", moves)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.#", "0"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "id", orderTestKey),
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_validation(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "summary"]
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile("duplicate values"),
			},
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", ""]
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile("length must be at least 1"),
			},
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = ""
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestTabID),
				ExpectError: regexp.MustCompile("length must be at least 1"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_membershipMismatch(t *testing.T) {
	restore := jira.SetScreenTabFieldOrderVerifySettings(500*time.Millisecond, 20*time.Millisecond)
	defer restore()
	mock := newScreenTabFieldOrderServer(t, []string{"summary", "customfield_1"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			_, moves, adds, removals := mock.counters()
			if moves != 0 || adds != 0 || removals != 0 {
				return fmt.Errorf("mismatch must fail before any write, got moves=%d adds=%d removals=%d", moves, adds, removals)
			}
			if got := mock.liveOrder(); strings.Join(got, ",") != "summary,customfield_1" {
				return fmt.Errorf("remote order must be untouched, got %v", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// Configured field not placed on the tab.
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile(`Configured\s+but\s+not\s+placed\s+on\s+the\s+tab`),
			},
			// Field on the tab that the configuration does not manage.
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile(`Placed\s+on\s+the\s+tab\s+but\s+not\s+configured`),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_readNotFound(t *testing.T) {
	restore := jira.SetScreenTabFieldOrderVerifySettings(500*time.Millisecond, 20*time.Millisecond)
	defer restore()
	mock := newScreenTabFieldOrderServer(t, []string{"summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	config := fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestScreenID, orderTestTabID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{Config: config},
			// Tab deleted out-of-band: Read must remove the resource (404), so
			// the follow-up plan recreates it and apply fails the preflight.
			{
				PreConfig:   mock.removeDefaultTab,
				Config:      config,
				ExpectError: regexp.MustCompile("not found"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_readForbidden(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	config := fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestScreenID, orderTestTabID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{Config: config},
			// A permission error on Read must surface as an error, not remove
			// the resource from state.
			{
				PreConfig: func() {
					mock.mu.Lock()
					mock.getStatus = func(int) int { return http.StatusForbidden }
					mock.mu.Unlock()
				},
				Config:      config,
				ExpectError: regexp.MustCompile("403"),
			},
			// With reads healthy again, the plan must be empty — proving the
			// resource was never removed during the 403 refresh.
			{
				PreConfig: func() {
					mock.mu.Lock()
					mock.getStatus = nil
					mock.mu.Unlock()
				},
				Config: config,
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_placementVisibility(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary", "customfield_1"})
	mock.getRawBody = func(gets int) string {
		if gets <= 2 {
			return `[{"id":"summary","name":"Summary"}]`
		}
		return ""
	}
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			gets, moves, adds, removals := mock.counters()
			if gets < 3 || moves != 0 || adds != 0 || removals != 0 {
				return fmt.Errorf("expected read-only membership convergence, got gets=%d moves=%d adds=%d removals=%d", gets, moves, adds, removals)
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1"]
}`, orderTestScreenID, orderTestTabID),
			Check: resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.1", "customfield_1"),
		}},
	})
}

func TestAccScreenTabFieldOrderResource_eventualConsistency(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"customfield_2", "customfield_1", "summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	// The first two reads after the moves return the stale pre-move order.
	mock.setStaleReads(2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if got := mock.liveOrder(); strings.Join(got, ",") != "summary,customfield_1,customfield_2" {
				return fmt.Errorf("expected converged order, got %v", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				Check: resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "summary"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_verificationTimeout(t *testing.T) {
	restore := jira.SetScreenTabFieldOrderVerifySettings(500*time.Millisecond, 20*time.Millisecond)
	defer restore()

	mock := newScreenTabFieldOrderServer(t, []string{"customfield_2", "customfield_1", "summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	// Reads never catch up: verification must fail bounded, without persisting
	// the planned order to state.
	mock.setStaleReads(1 << 30)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID),
				ExpectError: regexp.MustCompile("did not converge"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_partialFailureRecovers(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"customfield_2", "customfield_1", "summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	mock.mu.Lock()
	mock.moveStatus = func(nth int) int {
		if nth == 2 {
			return http.StatusForbidden
		}
		return 0
	}
	mock.mu.Unlock()

	config := fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1", "customfield_2"]
}`, orderTestScreenID, orderTestTabID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if got := mock.liveOrder(); strings.Join(got, ",") != "summary,customfield_1,customfield_2" {
				return fmt.Errorf("expected recovered order, got %v", got)
			}
			return nil
		},
		Steps: []resource.TestStep{
			// The second move fails; apply errors with a recovery hint.
			{
				Config:      config,
				ExpectError: regexp.MustCompile("1 of 3"),
			},
			// Healing the remote and re-applying the same config converges.
			{
				PreConfig: func() {
					mock.mu.Lock()
					mock.moveStatus = nil
					mock.mu.Unlock()
				},
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "summary"),
					func(*terraform.State) error {
						if got := mock.liveOrder(); strings.Join(got, ",") != "summary,customfield_1,customfield_2" {
							return fmt.Errorf("expected recovered order, got %v", got)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_malformedRemote(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate_remote_ids": `[{"id":"summary","name":"Summary"},{"id":"summary","name":"Summary"}]`,
		"empty_remote_id":      `[{"id":"","name":"Nameless"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			mock := newScreenTabFieldOrderServer(t, nil)
			mock.mu.Lock()
			mock.getRawBody = func(int) string { return body }
			mock.mu.Unlock()
			url := mock.start()
			setupScreenTabFieldOrderEnv(t, url)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
				CheckDestroy:             func(*terraform.State) error { return nil },
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestScreenID, orderTestTabID),
						ExpectError: regexp.MustCompile("refusing"),
					},
				},
			})
		})
	}
}

func TestAccScreenTabFieldOrderResource_import(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary", "customfield_1"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary", "customfield_1"]
}`, orderTestScreenID, orderTestTabID),
			},
			// Import reads the current remote order.
			{
				ResourceName:      "atlassian_jira_screen_tab_field_order.o",
				ImportState:       true,
				ImportStateId:     orderTestKey,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_importInvalidID(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, []string{"summary"})
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy:             func(*terraform.State) error { return nil },
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = %q
  tab_id    = %q
  field_ids = ["summary"]
}`, orderTestScreenID, orderTestTabID),
			},
			{
				ResourceName:  "atlassian_jira_screen_tab_field_order.o",
				ImportState:   true,
				ImportStateId: "just-a-screen-id",
				ExpectError:   regexp.MustCompile("Invalid import ID"),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_unknownFieldIDs(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	// Seed the tab with its own (plan-time unknown) ID plus summary, so the
	// list contains unknown elements during planning.
	mock.mu.Lock()
	mock.seedTabFields = func(screenID, tabID string) {
		mock.fields[screenID+"/"+tabID] = []string{tabID, "summary"}
	}
	mock.mu.Unlock()
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			_, moves, _, _ := mock.counters()
			// Both fields are moved to First in reverse order, matching len(field_ids).
			if moves != 2 {
				return fmt.Errorf("expected two moves (reverse-first over field_ids), got %d", moves)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: `
resource "atlassian_jira_screen" "s" {
  name = "Unknown Order Screen"
}
resource "atlassian_jira_screen_tab" "t" {
  screen_id = atlassian_jira_screen.s.id
  name      = "Unknown Order Tab"
}
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = atlassian_jira_screen.s.id
  tab_id    = atlassian_jira_screen_tab.t.id

  field_ids = ["summary", atlassian_jira_screen_tab.t.id]

  depends_on = [atlassian_jira_screen_tab.t]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "summary"),
					resource.TestCheckResourceAttrSet("atlassian_jira_screen_tab_field_order.o", "field_ids.1"),
				),
			},
		},
	})
}

func TestAccScreenTabFieldOrderResource_placementDependency(t *testing.T) {
	mock := newScreenTabFieldOrderServer(t, nil)
	url := mock.start()
	setupScreenTabFieldOrderEnv(t, url)

	var movesBeforeDestroy, removalsBeforeDestroy int

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testutil.ProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Placement resources own membership: both must be removed by their
			// own Delete, while the ordering resource must not reorder anything.
			_, moves, _, removals := mock.counters()
			if moves != movesBeforeDestroy {
				return fmt.Errorf("destroy must not reorder fields: moves %d -> %d", movesBeforeDestroy, moves)
			}
			if removals != removalsBeforeDestroy+2 {
				return fmt.Errorf("expected both placements removed by their owners, removals=%d", removals)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: `
resource "atlassian_jira_screen" "s" {
  name = "Placement Screen"
}
resource "atlassian_jira_screen_tab" "t" {
  screen_id = atlassian_jira_screen.s.id
  name      = "Placement Tab"
}
resource "atlassian_jira_screen_tab_field" "summary" {
  screen_id = atlassian_jira_screen.s.id
  tab_id    = atlassian_jira_screen_tab.t.id
  field_id  = "summary"
}
resource "atlassian_jira_screen_tab_field" "department" {
  screen_id = atlassian_jira_screen.s.id
  tab_id    = atlassian_jira_screen_tab.t.id
  field_id  = "customfield_10001"
}
resource "atlassian_jira_screen_tab_field_order" "o" {
  screen_id = atlassian_jira_screen.s.id
  tab_id    = atlassian_jira_screen_tab.t.id

  field_ids = [
    atlassian_jira_screen_tab_field.department.field_id,
    atlassian_jira_screen_tab_field.summary.field_id,
  ]

  depends_on = [
    atlassian_jira_screen_tab_field.summary,
    atlassian_jira_screen_tab_field.department,
  ]
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.0", "customfield_10001"),
					resource.TestCheckResourceAttr("atlassian_jira_screen_tab_field_order.o", "field_ids.1", "summary"),
					func(state *terraform.State) error {
						orderRes := state.RootModule().Resources["atlassian_jira_screen_tab_field_order.o"]
						if orderRes == nil {
							return fmt.Errorf("order resource missing from state")
						}
						screenID := orderRes.Primary.Attributes["screen_id"]
						tabID := orderRes.Primary.Attributes["tab_id"]

						_, moves, adds, removals := mock.counters()
						if adds != 2 {
							return fmt.Errorf("expected 2 placement adds, got %d", adds)
						}
						// Placement creation order is not deterministic (two
						// unordered placement resources), so the reconciliation is
						// either a no-op (0 moves) or a full reverse-first pass (2).
						if moves != 0 && moves != 2 {
							return fmt.Errorf("expected 0 or 2 moves to reorder both fields, got %d", moves)
						}
						if got := mock.liveOrderFor(screenID, tabID); strings.Join(got, ",") != "customfield_10001,summary" {
							return fmt.Errorf("expected ordered tab, got %v", got)
						}
						movesBeforeDestroy = moves
						removalsBeforeDestroy = removals
						return nil
					},
				),
			},
		},
	})
}
