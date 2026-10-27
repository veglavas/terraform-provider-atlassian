package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

type managedFieldScheme struct {
	ID          string  `json:"id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func readManagedFieldScheme(ctx context.Context, client *atlassian.Client, id string) (*managedFieldScheme, error) {
	var result managedFieldScheme
	status, err := fieldSchemeGet(ctx, client, fieldSchemesPath+"/"+atlassian.PathEscape(id), &result)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, nil
	}
	if result.ID != id || result.Name == nil || *result.Name == "" {
		return nil, fmt.Errorf("incomplete or mismatched field scheme response")
	}
	return &result, nil
}

// POST/PUT scheme responses contain numeric IDs, unlike the GET response.
// Preserve planned metadata: mutation responses can omit name/description.
func saveManagedFieldScheme(ctx context.Context, client *atlassian.Client, id, name, description string) (string, error) {
	method, apiPath := http.MethodPut, fieldSchemesPath+"/"+atlassian.PathEscape(id)
	if id == "" {
		method, apiPath = http.MethodPost, fieldSchemesPath
	}
	body, err := json.Marshal(map[string]string{"name": name, "description": description})
	if err != nil {
		return "", err
	}
	response, err := client.Do(ctx, method, apiPath, body, atlassian.WithExperimentalAPI())
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("field scheme %s returned HTTP %d", method, response.StatusCode)
	}
	var result struct {
		ID int64 `json:"id"`
	}
	// An update has a known identity even when its response is incomplete.
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		if id != "" {
			return id, fmt.Errorf("decoding updated field scheme response: %w", err)
		}
		return "", fmt.Errorf("decoding created field scheme response: %w", err)
	}
	if id != "" {
		if result.ID != 0 && strconv.FormatInt(result.ID, 10) != id {
			return id, fmt.Errorf("updated field scheme response has a mismatched ID")
		}
		return id, nil
	}
	if result.ID <= 0 {
		return "", fmt.Errorf("created field scheme response omitted a valid ID; creation may have succeeded, inspect the tenant before retrying")
	}
	return strconv.FormatInt(result.ID, 10), nil
}

// Every bulk request is deliberately scoped to exactly one association.
func writeFieldSchemeAssociation(ctx context.Context, client *atlassian.Client, schemeID, fieldID string, workTypes []string, remove bool) error {
	scheme, err := positiveID(schemeID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(workTypes))
	for _, id := range workTypes {
		n, err := positiveID(id)
		if err != nil {
			return err
		}
		ids = append(ids, n)
	}
	method := http.MethodPut
	item := map[string]any{"schemeIds": []int64{scheme}}
	var body any
	if remove {
		method = http.MethodDelete
		body = map[string]any{fieldID: item}
	} else {
		// The contract explicitly defines omission as availability to all work
		// types, replacing prior restrictions. Do not guess empty-list semantics.
		if len(ids) > 0 {
			item["restrictedToWorkTypes"] = ids
		}
		body = map[string]any{fieldID: []any{item}}
	}
	return writeFieldSchemeMapping(ctx, client, method, fieldSchemesPath+"/fields", body, scheme, fieldID, 0, ids)
}
func assignFieldSchemeProject(ctx context.Context, client *atlassian.Client, schemeID, projectID string) error {
	scheme, err := positiveID(schemeID)
	if err != nil {
		return err
	}
	project, err := positiveID(projectID)
	if err != nil {
		return err
	}
	return writeFieldSchemeMapping(ctx, client, http.MethodPut, fieldSchemesPath+"/projects", map[string]any{schemeID: map[string]any{"projectIds": []int64{project}}}, scheme, "", project, nil)
}
func writeFieldSchemeMapping(ctx context.Context, client *atlassian.Client, method, apiPath string, body any, scheme int64, field string, project int64, workTypes []int64) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response, err := client.Do(ctx, method, apiPath, data, atlassian.WithExperimentalAPI())
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("field scheme association %s returned HTTP %d", method, response.StatusCode)
	}
	var result struct {
		Results []struct {
			SchemeID    int64           `json:"schemeId"`
			FieldID     string          `json:"fieldId"`
			ProjectID   int64           `json:"projectId"`
			WorkTypeIDs []int64         `json:"workTypeIds"`
			Success     *bool           `json:"success"`
			Error       json.RawMessage `json:"error"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding field scheme association result: %w", err)
	}
	if len(result.Results) != 1 {
		return fmt.Errorf("expected one field scheme association result")
	}
	r := result.Results[0]
	if r.SchemeID != scheme || r.FieldID != field || r.ProjectID != project || r.Success == nil || !*r.Success || (len(r.Error) > 0 && string(r.Error) != "null") {
		return fmt.Errorf("field scheme association failed or returned mismatched results")
	}
	if field != "" && method == http.MethodPut {
		if r.WorkTypeIDs == nil || len(r.WorkTypeIDs) != len(workTypes) {
			return fmt.Errorf("field scheme association returned mismatched work types")
		}
		seen := make(map[int64]bool)
		for _, id := range r.WorkTypeIDs {
			if seen[id] {
				return fmt.Errorf("duplicate work type in field scheme association result")
			}
			seen[id] = true
		}
		for _, id := range workTypes {
			if !seen[id] {
				return fmt.Errorf("field scheme association returned mismatched work types")
			}
		}
	}
	return nil
}
func readManagedFieldAssociation(ctx context.Context, client *atlassian.Client, schemeID, fieldID string) (*fieldSchemeField, error) {
	fields, missing, err := fieldSchemePages[fieldSchemeField](ctx, client, fieldSchemesPath+"/"+atlassian.PathEscape(schemeID)+"/fields?fieldId="+atlassian.QueryEscape(fieldID))
	if err != nil {
		return nil, err
	}
	if missing {
		parent, err := readManagedFieldScheme(ctx, client, schemeID)
		if err != nil {
			return nil, err
		}
		if parent == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("field scheme exists but field discovery is unavailable; experimental feature may be disabled")
	}
	var found *fieldSchemeField
	for i := range fields {
		f := &fields[i]
		if f.FieldID != fieldID || found != nil {
			return nil, fmt.Errorf("mismatched or duplicate field association response")
		}
		seen := make(map[string]bool)
		for _, id := range f.RestrictedToWorkTypes {
			if _, err := positiveID(id); err != nil {
				return nil, fmt.Errorf("invalid issue type restriction in field association response")
			}
			if seen[id] {
				return nil, fmt.Errorf("duplicate issue type restriction")
			}
			seen[id] = true
		}
		found = f
	}
	return found, nil
}
func readManagedProjectFieldScheme(ctx context.Context, client *atlassian.Client, projectID string) (string, error) {
	entries, missing, err := fieldSchemePages[struct {
		ProjectID int64 `json:"projectId"`
		SchemeID  int64 `json:"schemeId"`
	}](ctx, client, fieldSchemesPath+"/projects?projectId="+atlassian.QueryEscape(projectID))
	if err != nil {
		return "", err
	}
	if missing {
		return "", fmt.Errorf("field scheme project discovery is unavailable; experimental feature may be disabled")
	}
	found := ""
	for _, entry := range entries {
		if entry.ProjectID <= 0 || entry.SchemeID <= 0 || strconv.FormatInt(entry.ProjectID, 10) != projectID || found != "" {
			return "", fmt.Errorf("invalid or duplicate project field scheme mapping")
		}
		found = strconv.FormatInt(entry.SchemeID, 10)
	}
	return found, nil
}
