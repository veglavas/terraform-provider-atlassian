package jira

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Jira does not promise to return statuses or transition links in request order.
// Preserve the configured order of surviving IDs, but still expose additions and
// removals as drift. The first configured status remains the initial status.
func stableWorkflowIDs(ctx context.Context, prior types.List, current []string) []string {
	if prior.IsNull() || prior.IsUnknown() {
		return current
	}
	var old []string
	if d := prior.ElementsAs(ctx, &old, false); d.HasError() {
		return current
	}
	present := map[string]bool{}
	for _, id := range current {
		present[id] = true
	}
	result := make([]string, 0, len(current))
	for _, id := range old {
		if present[id] {
			result = append(result, id)
			delete(present, id)
		}
	}
	for _, id := range current {
		if present[id] {
			result = append(result, id)
			delete(present, id)
		}
	}
	return result
}
