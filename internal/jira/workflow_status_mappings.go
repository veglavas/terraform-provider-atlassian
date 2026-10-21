package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func requireRemovedWorkflowMappings(ctx context.Context, current workflowDefinition, desired []string, configured types.Map) error {
	var statuses []workflowDefinition
	if err := json.Unmarshal(current["statuses"], &statuses); err != nil {
		return err
	}
	mappings := map[string]string{}
	if !configured.IsNull() {
		if d := configured.ElementsAs(ctx, &mappings, false); d.HasError() {
			return fmt.Errorf("invalid status_mappings: %s", d)
		}
	}
	retained := map[string]bool{}
	for _, id := range desired {
		retained[id] = true
	}
	for _, status := range statuses {
		id := definitionString(status, "statusReference")
		if !retained[id] && !retained[mappings[id]] {
			return fmt.Errorf("removing status %s requires an explicit status_mappings destination present in statuses; no issues were migrated", id)
		}
	}
	return nil
}

func applyWorkflowStatusMappings(ctx context.Context, payload map[string]any, configured types.Map) error {
	if configured.IsNull() {
		return nil
	}
	mappings := map[string]string{}
	if d := configured.ElementsAs(ctx, &mappings, false); d.HasError() {
		return fmt.Errorf("invalid status_mappings: %s", d)
	}
	ids := make([]string, 0, len(mappings))
	for id := range mappings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]map[string]string, 0, len(ids))
	for _, old := range ids {
		newID := mappings[old]
		if old == "" || newID == "" {
			return fmt.Errorf("status mapping IDs must not be empty")
		}
		result = append(result, map[string]string{"oldStatusReference": old, "newStatusReference": newID})
	}
	payload["workflows"].([]workflowDefinition)[0]["defaultStatusMappings"] = rawJSON(result)
	return nil
}
