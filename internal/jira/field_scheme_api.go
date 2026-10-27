package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

const fieldSchemesPath = "/rest/api/3/config/fieldschemes"

type fieldRequirementKey struct {
	SchemeID    string
	FieldID     string
	IssueTypeID string
}

func positiveID(id string) (int64, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return 0, fmt.Errorf("expected a canonical positive numeric ID, got %q", id)
	}
	return n, nil
}

// Field schemes are experimental. Keep status-aware reads and strict collection
// validation scoped here; an incomplete page must never look like deletion.
func fieldSchemePages[T any](ctx context.Context, client *atlassian.Client, apiPath string) ([]T, bool, error) {
	var values []T
	for start, pages := 0, 0; pages < 1000; pages++ {
		var page struct {
			Values  []T   `json:"values"`
			StartAt *int  `json:"startAt"`
			IsLast  *bool `json:"isLast"`
			Total   *int  `json:"total"`
		}
		sep := "?"
		if strings.Contains(apiPath, "?") {
			sep = "&"
		}
		status, err := fieldSchemeGet(ctx, client, fmt.Sprintf("%s%sstartAt=%d&maxResults=100", apiPath, sep, start), &page)
		if err != nil {
			return nil, false, err
		}
		if status == http.StatusNotFound {
			return nil, true, nil
		}
		if page.Values == nil || page.StartAt == nil || *page.StartAt != start || (page.IsLast == nil && page.Total == nil) {
			return nil, false, fmt.Errorf("incomplete field scheme collection response")
		}
		next := start + len(page.Values)
		if page.Total != nil && (*page.Total < next || (page.IsLast != nil && *page.IsLast && next != *page.Total) || (page.IsLast != nil && !*page.IsLast && next == *page.Total)) {
			return nil, false, fmt.Errorf("inconsistent field scheme pagination")
		}
		last := (page.IsLast != nil && *page.IsLast) || (page.Total != nil && next == *page.Total)
		if !last && len(page.Values) == 0 {
			return nil, false, fmt.Errorf("empty nonfinal field scheme page")
		}
		values = append(values, page.Values...)
		if last {
			return values, false, nil
		}
		start = next
	}
	return nil, false, fmt.Errorf("field scheme pagination exceeded 1000 pages")
}

// Status-aware experimental GET; server response bodies are not echoed in errors.
func fieldSchemeGet(ctx context.Context, client *atlassian.Client, apiPath string, result any) (int, error) {
	resp, err := client.Do(ctx, http.MethodGet, apiPath, nil, atlassian.WithExperimentalAPI())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("field scheme API GET returned HTTP %d", resp.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return resp.StatusCode, fmt.Errorf("decoding field scheme response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

type fieldSchemeField struct {
	FieldID    string `json:"fieldId"`
	Parameters struct {
		IsRequired *bool `json:"isRequired"`
	} `json:"parameters"`
	RestrictedToWorkTypes []string `json:"restrictedToWorkTypes"`
	WorkTypeParameters    []struct {
		WorkTypeID string `json:"workTypeId"`
		IsRequired *bool  `json:"isRequired"`
	} `json:"workTypeParameters"`
}

// Reads the effective requirement, not an assertion that an explicit override
// exists. If the override is absent the scheme's default remains authoritative.
func readFieldRequirement(ctx context.Context, client *atlassian.Client, key fieldRequirementKey) (bool, bool, error) {
	fields, missing, err := fieldSchemePages[fieldSchemeField](ctx, client, fieldSchemesPath+"/"+atlassian.PathEscape(key.SchemeID)+"/fields?fieldId="+atlassian.QueryEscape(key.FieldID))
	if err != nil {
		return false, false, err
	}
	if missing {
		// The field-list API documents 404 for both a missing scheme and a
		// disabled feature flag. Only the direct scheme lookup disambiguates.
		var scheme projectFieldScheme
		status, err := fieldSchemeGet(ctx, client, fieldSchemesPath+"/"+atlassian.PathEscape(key.SchemeID), &scheme)
		if err != nil {
			return false, false, err
		}
		if status == http.StatusNotFound {
			return false, false, nil
		}
		return false, false, fmt.Errorf("field scheme exists but field discovery returned HTTP 404; the experimental feature may be disabled or unavailable")
	}
	var found *fieldSchemeField
	for i := range fields {
		if fields[i].FieldID == "" {
			return false, false, fmt.Errorf("field scheme response contains a field without an ID")
		}
		if fields[i].FieldID != key.FieldID {
			return false, false, fmt.Errorf("field scheme response did not respect fieldId filter")
		}
		if found != nil {
			return false, false, fmt.Errorf("duplicate field in field scheme response")
		}
		found = &fields[i]
	}
	if found == nil {
		return false, false, nil
	}
	if len(found.RestrictedToWorkTypes) > 0 {
		allowed := false
		for _, id := range found.RestrictedToWorkTypes {
			if id == key.IssueTypeID {
				allowed = true
			}
		}
		if !allowed {
			return false, false, nil
		}
	}
	required := found.Parameters.IsRequired
	seen := make(map[string]bool)
	for _, p := range found.WorkTypeParameters {
		if _, err := positiveID(p.WorkTypeID); err != nil || seen[p.WorkTypeID] {
			return false, false, fmt.Errorf("invalid or duplicate work type in field scheme response")
		}
		seen[p.WorkTypeID] = true
		if p.WorkTypeID == key.IssueTypeID && p.IsRequired != nil {
			required = p.IsRequired
		}
	}
	if required == nil {
		// SearchResultFieldParameters is optional in the public contract.
		// Consult the parameter endpoint's stronger contract instead of
		// guessing that an omitted requirement means false.
		return readFieldRequirementParameters(ctx, client, key)
	}
	return *required, true, nil
}

func readFieldRequirementParameters(ctx context.Context, client *atlassian.Client, key fieldRequirementKey) (bool, bool, error) {
	var params struct {
		FieldID    string `json:"fieldId"`
		Parameters struct {
			IsRequired *bool `json:"isRequired"`
		} `json:"parameters"`
		WorkTypeParameters []struct {
			WorkTypeID int64 `json:"workTypeId"`
			IsRequired *bool `json:"isRequired"`
		} `json:"workTypeParameters"`
	}
	status, err := fieldSchemeGet(ctx, client, fieldSchemesPath+"/"+atlassian.PathEscape(key.SchemeID)+"/fields/"+atlassian.PathEscape(key.FieldID)+"/parameters", &params)
	if err != nil {
		return false, false, err
	}
	if status == http.StatusNotFound || params.FieldID != key.FieldID {
		return false, false, fmt.Errorf("missing or mismatched field parameters for an existing field association")
	}
	required := params.Parameters.IsRequired
	seen := make(map[int64]bool)
	for _, p := range params.WorkTypeParameters {
		if p.WorkTypeID <= 0 || p.IsRequired == nil || seen[p.WorkTypeID] {
			return false, false, fmt.Errorf("invalid or duplicate work type in field parameter response")
		}
		seen[p.WorkTypeID] = true
		if strconv.FormatInt(p.WorkTypeID, 10) == key.IssueTypeID {
			required = p.IsRequired
		}
	}
	if required == nil {
		return false, false, fmt.Errorf("field parameter response omitted the effective isRequired parameter")
	}
	return *required, true, nil
}

type fieldSchemeWriteResults struct {
	Results []struct {
		FieldID     string          `json:"fieldId"`
		SchemeID    int64           `json:"schemeId"`
		WorkTypeID  *int64          `json:"workTypeId"`
		WorkTypeIDs []int64         `json:"workTypeIds"`
		Success     *bool           `json:"success"`
		Error       json.RawMessage `json:"error"`
	} `json:"results"`
}

func writeFieldRequirement(ctx context.Context, client *atlassian.Client, key fieldRequirementKey, required *bool) error {
	schemeID, err := positiveID(key.SchemeID)
	if err != nil {
		return err
	}
	issueTypeID, err := positiveID(key.IssueTypeID)
	if err != nil {
		return err
	}
	var body any
	method := http.MethodPut
	if required != nil {
		body = map[string]any{key.FieldID: []any{map[string]any{
			"schemeIds":          []int64{schemeID},
			"workTypeParameters": []any{map[string]any{"workTypeId": issueTypeID, "isRequired": *required}},
		}}}
	} else {
		method = http.MethodDelete
		body = map[string]any{key.FieldID: []any{map[string]any{
			"schemeId": schemeID, "workTypeIds": []int64{issueTypeID}, "parameters": []string{"isRequired"},
		}}}
	}
	// Unlike Client.DeleteWithStatus, this bulk DELETE requires a JSON body and
	// inspection of a 207 response. Do buffers bodies and reapplies the header
	// on retries; keep status/partial-result handling scoped to this API.
	// DELETE 200 is documented without a body; PUT 200 requires results.
	// Decode locally so an empty DELETE 200 is accepted but an empty 207 is not.
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	resp, err := client.Do(ctx, method, fieldSchemesPath+"/fields/parameters", data, atlassian.WithExperimentalAPI())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("field scheme parameter %s returned HTTP %d", method, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	var result fieldSchemeWriteResults
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err == io.EOF && method == http.MethodDelete && resp.StatusCode == http.StatusOK {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decoding field scheme parameter result: %w", err)
	}
	if method == http.MethodDelete && resp.StatusCode == http.StatusOK && len(result.Results) == 0 {
		return nil
	}
	if len(result.Results) != 1 {
		return fmt.Errorf("field scheme parameter write returned incomplete or unexpected results")
	}
	r := result.Results[0]
	if r.FieldID != key.FieldID || r.SchemeID != schemeID || (r.WorkTypeID != nil && *r.WorkTypeID != issueTypeID) || (len(r.WorkTypeIDs) != 0 && (len(r.WorkTypeIDs) != 1 || r.WorkTypeIDs[0] != issueTypeID)) || r.Success == nil {
		return fmt.Errorf("field scheme parameter write returned mismatched results")
	}
	if !*r.Success || (len(r.Error) != 0 && string(r.Error) != "null" && string(r.Error) != `""`) {
		return fmt.Errorf("field scheme parameter write was unsuccessful (HTTP %d); refresh and retry", resp.StatusCode)
	}
	return nil
}

type projectFieldScheme struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func readProjectFieldScheme(ctx context.Context, client *atlassian.Client, projectID string) (*projectFieldScheme, error) {
	mappings, missing, err := fieldSchemePages[struct {
		ProjectID int64 `json:"projectId"`
		SchemeID  int64 `json:"schemeId"`
	}](ctx, client, fieldSchemesPath+"/projects?projectId="+atlassian.QueryEscape(projectID))
	if err != nil {
		return nil, err
	}
	if missing {
		return nil, fmt.Errorf("field scheme project discovery returned HTTP 404: the experimental Field schemes feature is disabled or unavailable")
	}
	var schemeID string
	for _, m := range mappings {
		if strconv.FormatInt(m.ProjectID, 10) != projectID || m.SchemeID <= 0 || schemeID != "" {
			return nil, fmt.Errorf("invalid or ambiguous project field scheme mapping")
		}
		schemeID = strconv.FormatInt(m.SchemeID, 10)
	}
	if schemeID == "" {
		return nil, fmt.Errorf("no field scheme found for project %s", projectID)
	}
	var scheme projectFieldScheme
	status, err := fieldSchemeGet(ctx, client, fieldSchemesPath+"/"+schemeID, &scheme)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound || scheme.ID != schemeID || scheme.Name == "" {
		return nil, fmt.Errorf("missing or mismatched project field scheme")
	}
	return &scheme, nil
}
