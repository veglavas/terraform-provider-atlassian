package jira

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func fieldContextsPath(fieldID string) string {
	return "/rest/api/3/field/" + atlassian.PathEscape(fieldID) + "/context"
}

func fieldContextPath(fieldID, contextID string) string {
	return fieldContextsPath(fieldID) + "/" + atlassian.PathEscape(contextID)
}

type fieldContextAPI struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	IsGlobal       bool   `json:"isGlobalContext"`
	IsAnyIssueType bool   `json:"isAnyIssueType"`
}

type fieldContextWrite struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ProjectIDs   []string `json:"projectIds"`
	IssueTypeIDs []string `json:"issueTypeIds"`
}

type fieldContextMapping struct {
	ContextID   string `json:"contextId"`
	ProjectID   string `json:"projectId"`
	IssueTypeID string `json:"issueTypeId"`
}

type fieldOptionAPI struct {
	ID       string `json:"id,omitempty"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
	ParentID string `json:"optionId,omitempty"`
}

// Jira has no individual GET for contexts/options. These endpoints must be
// scanned, including all pages. A missing parent is distinct from an API error.
func fieldContextPages[T any](ctx context.Context, client *atlassian.Client, apiPath string) ([]T, int, error) {
	var values []T
	start := 0
	const maxPages = 1000
	for n := 0; n < maxPages; n++ {
		separator := "?"
		if strings.Contains(apiPath, "?") {
			separator = "&"
		}
		var page struct {
			Values  []T   `json:"values"`
			StartAt int   `json:"startAt"`
			IsLast  *bool `json:"isLast"`
			Total   *int  `json:"total"`
		}
		status, err := client.GetWithStatus(ctx, fmt.Sprintf("%s%sstartAt=%d&maxResults=100", apiPath, separator, start), &page)
		if err != nil || status == http.StatusNotFound {
			return nil, status, err
		}
		if start > 0 && page.StartAt != start {
			return nil, status, fmt.Errorf("pagination for %s returned startAt=%d, expected %d", apiPath, page.StartAt, start)
		}
		// Absence is authoritative only after a complete, well-formed scan.
		// In particular, a missing/null values array must not look like deletion.
		if page.Values == nil || (page.IsLast == nil && page.Total == nil) {
			return nil, status, fmt.Errorf("incomplete collection response for %s", apiPath)
		}
		next := start + len(page.Values)
		if page.Total != nil && (*page.Total < next || (page.IsLast != nil && *page.IsLast && next < *page.Total)) {
			return nil, status, fmt.Errorf("inconsistent pagination total for %s", apiPath)
		}
		last := (page.IsLast != nil && *page.IsLast) || (page.Total != nil && next == *page.Total)
		if !last && len(page.Values) == 0 {
			return nil, status, fmt.Errorf("incomplete empty page for %s", apiPath)
		}
		values = append(values, page.Values...)
		start = next
		if last {
			return values, status, nil
		}
	}
	return nil, 0, fmt.Errorf("pagination exceeded %d pages for %s", maxPages, apiPath)
}

func findFieldContext(ctx context.Context, client *atlassian.Client, fieldID, id, name string) (*fieldContextAPI, error) {
	apiPath := fieldContextsPath(fieldID)
	if id != "" {
		apiPath += "?contextId=" + atlassian.QueryEscape(id)
	}
	contexts, _, err := fieldContextPages[fieldContextAPI](ctx, client, apiPath)
	if err != nil {
		return nil, err
	}
	var found *fieldContextAPI
	for _, c := range contexts {
		if (id != "" && c.ID == id) || (id == "" && c.Name == name) {
			if found != nil {
				return nil, fmt.Errorf("multiple contexts named %q; look up by id instead", name)
			}
			copy := c
			found = &copy
		}
	}
	return found, nil
}

func readFieldContextScope(ctx context.Context, client *atlassian.Client, fieldID string, c fieldContextAPI) (projects, issueTypes []string, missing bool, err error) {
	projects, issueTypes = []string{}, []string{}
	for _, scope := range []struct {
		endpoint string
		needed   bool
	}{{"projectmapping", !c.IsGlobal}, {"issuetypemapping", !c.IsAnyIssueType}} {
		if !scope.needed {
			continue
		}
		mappings, status, readErr := fieldContextPages[fieldContextMapping](ctx, client, fieldContextsPath(fieldID)+"/"+scope.endpoint+"?contextId="+atlassian.QueryEscape(c.ID))
		if readErr != nil {
			return nil, nil, false, readErr
		}
		if status == http.StatusNotFound {
			return nil, nil, true, nil
		}
		for _, m := range mappings {
			if m.ContextID != c.ID {
				continue
			}
			if scope.endpoint == "projectmapping" && m.ProjectID != "" {
				projects = append(projects, m.ProjectID)
			}
			if scope.endpoint == "issuetypemapping" && m.IssueTypeID != "" {
				issueTypes = append(issueTypes, m.IssueTypeID)
			}
		}
		// Never translate a partial/eventually consistent scoped response into a
		// global/all-issue-types context in Terraform state.
		if (scope.endpoint == "projectmapping" && len(projects) == 0) || (scope.endpoint == "issuetypemapping" && len(issueTypes) == 0) {
			return nil, nil, false, fmt.Errorf("scope mappings for context %s are not available; retry the refresh", c.ID)
		}
	}
	return projects, issueTypes, false, nil
}

func findFieldOption(ctx context.Context, client *atlassian.Client, fieldID, contextID, id string) (*fieldOptionAPI, error) {
	options, _, err := fieldContextPages[fieldOptionAPI](ctx, client, fieldContextPath(fieldID, contextID)+"/option")
	if err != nil {
		return nil, err
	}
	for _, option := range options {
		if option.ID == id {
			return &option, nil
		}
	}
	return nil, nil
}

// Wait only after writes; normal reads must still detect out-of-band deletion.
func waitForFieldConfiguration(ctx context.Context, check func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		ready, err := check(ctx)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("waiting for Jira field configuration: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// Jira accepts bulk option writes. Serialize writes within a context so parallel
// parent/sibling resources cannot race. Entries are released after their last
// waiter; different contexts/providers can still run concurrently. This is an
// in-process lock, not a replacement for Terraform's remote state lock.
var fieldContextLocks = struct {
	sync.Mutex
	entries map[string]*fieldContextLock
}{entries: make(map[string]*fieldContextLock)}

type fieldContextLock struct {
	sync.Mutex
	users int
}

func lockFieldContext(client *atlassian.Client, apiPath string) func() {
	key := client.BaseURL() + apiPath
	fieldContextLocks.Lock()
	entry := fieldContextLocks.entries[key]
	if entry == nil {
		entry = &fieldContextLock{}
		fieldContextLocks.entries[key] = entry
	}
	entry.users++
	fieldContextLocks.Unlock()
	entry.Lock()
	return func() {
		entry.Unlock()
		fieldContextLocks.Lock()
		entry.users--
		if entry.users == 0 {
			delete(fieldContextLocks.entries, key)
		}
		fieldContextLocks.Unlock()
	}
}
