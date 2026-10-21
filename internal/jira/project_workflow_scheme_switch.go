package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func (r *projectWorkflowSchemeResource) switchScheme(ctx context.Context, projectID, targetID string, mappings types.Map) error {
	var page atlassian.PageResponse
	if err := r.client.Get(ctx, "/rest/api/3/workflowscheme/project?projectId="+atlassian.QueryEscape(projectID), &page); err != nil {
		return err
	}
	if len(page.Values) != 1 {
		return fmt.Errorf("expected exactly one current workflow scheme for project %s, got %d", projectID, len(page.Values))
	}
	var association projectWorkflowSchemeListEntry
	if err := json.Unmarshal(page.Values[0], &association); err != nil {
		return err
	}
	sourceID := fmt.Sprint(association.WorkflowScheme.ID)
	if sourceID == targetID {
		return nil
	}
	target, err := readModernScheme(ctx, r.client, targetID)
	if err != nil {
		return err
	}
	prepared, err := prepareSchemeMappings(ctx, r.client, sourceID, target.DefaultWorkflow.ID, target.associations(), mappings)
	if err != nil {
		return err
	}
	if err := runSchemeMutation(ctx, r.client, "/rest/api/3/workflowscheme/project/switch", map[string]any{"projectId": projectID, "targetSchemeId": targetID, "mappingsByIssueTypeOverride": prepared}); err != nil {
		return err
	}
	// Jira may complete the task before the association read replica catches up.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		var refreshed atlassian.PageResponse
		if err := r.client.Get(ctx, "/rest/api/3/workflowscheme/project?projectId="+atlassian.QueryEscape(projectID), &refreshed); err != nil {
			return err
		}
		if len(refreshed.Values) != 1 {
			return fmt.Errorf("switch completed but project association is unavailable; refresh before retrying")
		}
		if err := json.Unmarshal(refreshed.Values[0], &association); err != nil {
			return err
		}
		if fmt.Sprint(association.WorkflowScheme.ID) == targetID {
			return nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("switch completed but project %s is not assigned to scheme %s; refresh before retrying: %w", projectID, targetID, ctx.Err())
		case <-timer.C:
		}
	}
}
