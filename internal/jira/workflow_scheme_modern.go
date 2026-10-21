package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

func migrationMappingsSchema() schema.MapAttribute {
	return schema.MapAttribute{Optional: true, ElementType: types.MapType{ElemType: types.StringType}, Description: "Explicit issue status migrations: issue type ID => old status ID => new status ID. No migration destinations are chosen automatically."}
}

type schemeWorkflowAssociation struct {
	IssueTypeIDs []string `json:"issueTypeIds"`
	WorkflowID   string   `json:"workflowId"`
}
type modernWorkflowScheme struct {
	ID              string          `json:"id"`
	Version         json.RawMessage `json:"version"`
	TaskID          string          `json:"taskId"`
	DefaultWorkflow struct {
		ID string `json:"id"`
	} `json:"defaultWorkflow"`
	Workflows []struct {
		IssueTypeIDs []string `json:"issueTypeIds"`
		Workflow     struct {
			ID string `json:"id"`
		} `json:"workflow"`
	} `json:"workflowsForIssueTypes"`
}

func readModernScheme(ctx context.Context, c *atlassian.Client, id string) (*modernWorkflowScheme, error) {
	var schemes []modernWorkflowScheme
	if err := c.Post(ctx, "/rest/api/3/workflowscheme/read", map[string]any{"workflowSchemeIds": []string{id}}, &schemes); err != nil {
		return nil, err
	}
	for _, s := range schemes {
		if s.ID == id {
			if s.TaskID != "" {
				if err := waitWorkflowTask(ctx, c, s.TaskID); err != nil {
					return nil, err
				}
				var refreshed []modernWorkflowScheme
				if err := c.Post(ctx, "/rest/api/3/workflowscheme/read", map[string]any{"workflowSchemeIds": []string{id}}, &refreshed); err != nil {
					return nil, err
				}
				for _, latest := range refreshed {
					if latest.ID == id {
						if latest.TaskID != "" && latest.TaskID != s.TaskID {
							return nil, fmt.Errorf("another task started for workflow scheme %s; refresh before retrying", id)
						}
						return &latest, nil
					}
				}
				return nil, fmt.Errorf("scheme %s disappeared after migration", id)
			}
			return &s, nil
		}
	}
	return nil, fmt.Errorf("workflow scheme %s not found", id)
}
func (s *modernWorkflowScheme) associations() []schemeWorkflowAssociation {
	result := make([]schemeWorkflowAssociation, 0, len(s.Workflows))
	for _, w := range s.Workflows {
		result = append(result, schemeWorkflowAssociation{IssueTypeIDs: w.IssueTypeIDs, WorkflowID: w.Workflow.ID})
	}
	return result
}
func workflowIDByName(ctx context.Context, c *atlassian.Client, name string) (string, error) {
	var result workflowDefinitions
	if err := c.Post(ctx, "/rest/api/3/workflows", map[string]any{"workflowNames": []string{name}}, &result); err != nil {
		return "", err
	}
	for _, w := range result.Workflows {
		if definitionString(w, "name") == name {
			return definitionString(w, "id"), nil
		}
	}
	return "", fmt.Errorf("workflow %q not found", name)
}

type issueStatusMapping struct {
	IssueTypeID    string            `json:"issueTypeId"`
	StatusMappings []statusMigration `json:"statusMappings"`
}
type statusMigration struct {
	Old string `json:"oldStatusId"`
	New string `json:"newStatusId"`
}

func prepareSchemeMappings(ctx context.Context, c *atlassian.Client, sourceID, defaultID string, associations []schemeWorkflowAssociation, configured types.Map) ([]issueStatusMapping, error) {
	mappings := map[string]map[string]string{}
	if !configured.IsNull() {
		if d := configured.ElementsAs(ctx, &mappings, false); d.HasError() {
			return nil, fmt.Errorf("invalid status_mappings: %s", d)
		}
	}
	var required struct {
		ByIssueTypes []struct {
			IssueTypeID string   `json:"issueTypeId"`
			StatusIDs   []string `json:"statusIds"`
		} `json:"statusMappingsByIssueTypes"`
		ByWorkflows []struct {
			StatusIDs []string `json:"statusIds"`
		} `json:"statusMappingsByWorkflows"`
	}
	if err := c.Post(ctx, "/rest/api/3/workflowscheme/update/mappings", map[string]any{"id": sourceID, "defaultWorkflowId": defaultID, "workflowsForIssueTypes": associations}, &required); err != nil {
		return nil, err
	}
	if len(required.ByIssueTypes) == 0 {
		for _, requirement := range required.ByWorkflows {
			if len(requirement.StatusIDs) > 0 {
				return nil, fmt.Errorf("workflow-level migrations are required by Jira but returned no issue-type requirements; refusing to choose implicit migration destinations")
			}
		}
	}
	for _, requirement := range required.ByIssueTypes {
		for _, old := range requirement.StatusIDs {
			if mappings[requirement.IssueTypeID][old] == "" {
				return nil, fmt.Errorf("explicit status_mappings[%q][%q] required; no issues were migrated", requirement.IssueTypeID, old)
			}
		}
	}
	result := make([]issueStatusMapping, 0, len(mappings))
	issueTypes := make([]string, 0, len(mappings))
	for issueType := range mappings {
		issueTypes = append(issueTypes, issueType)
	}
	sort.Strings(issueTypes)
	for _, issueType := range issueTypes {
		entry := issueStatusMapping{IssueTypeID: issueType, StatusMappings: []statusMigration{}}
		oldIDs := make([]string, 0, len(mappings[issueType]))
		for old := range mappings[issueType] {
			oldIDs = append(oldIDs, old)
		}
		sort.Strings(oldIDs)
		for _, old := range oldIDs {
			newID := mappings[issueType][old]
			if old == "" || newID == "" {
				return nil, fmt.Errorf("status mapping IDs must not be empty")
			}
			entry.StatusMappings = append(entry.StatusMappings, statusMigration{Old: old, New: newID})
		}
		result = append(result, entry)
	}
	return result, nil
}
func runSchemeMutation(ctx context.Context, c *atlassian.Client, path string, body any) error {
	task, err := c.PostAsync(ctx, path, body)
	if err != nil {
		return err
	}
	if task != "" {
		return waitWorkflowTask(ctx, c, task)
	}
	return nil
}
func updateModernScheme(ctx context.Context, c *atlassian.Client, id string, plan workflowSchemeResourceModel) error {
	current, err := readModernScheme(ctx, c, id)
	if err != nil {
		return err
	}
	if len(current.Version) == 0 || string(current.Version) == "null" {
		return fmt.Errorf("scheme has no version; refusing unsafe update")
	}
	defaultID := current.DefaultWorkflow.ID
	if !plan.DefaultWorkflow.IsNull() && !plan.DefaultWorkflow.IsUnknown() {
		defaultID, err = workflowIDByName(ctx, c, plan.DefaultWorkflow.ValueString())
		if err != nil {
			return err
		}
	}
	associations := current.associations()
	if !plan.IssueTypeMappings.IsNull() && !plan.IssueTypeMappings.IsUnknown() {
		names := map[string]string{}
		if d := plan.IssueTypeMappings.ElementsAs(ctx, &names, false); d.HasError() {
			return fmt.Errorf("invalid issue type mappings: %s", d)
		}
		associations = []schemeWorkflowAssociation{}
		for issueType, name := range names {
			workflowID, err := workflowIDByName(ctx, c, name)
			if err != nil {
				return err
			}
			associations = append(associations, schemeWorkflowAssociation{IssueTypeIDs: []string{issueType}, WorkflowID: workflowID})
		}
	}
	mappings, err := prepareSchemeMappings(ctx, c, id, defaultID, associations, plan.StatusMappings)
	if err != nil {
		return err
	}
	return runSchemeMutation(ctx, c, "/rest/api/3/workflowscheme/update", map[string]any{"id": id, "version": current.Version, "name": plan.Name.ValueString(), "description": plan.Description.ValueString(), "defaultWorkflowId": defaultID, "workflowsForIssueTypes": associations, "statusMappingsByIssueTypeOverride": mappings})
}
