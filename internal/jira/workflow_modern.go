package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/lbajsarowicz/terraform-provider-atlassian/internal/atlassian"
)

// Keep API definitions lossless: rules, plugin configuration and layout fields
// not exposed in Terraform must survive a read-modify-write update.
type workflowDefinition map[string]json.RawMessage

type workflowDefinitions struct {
	Workflows []workflowDefinition `json:"workflows"`
	TaskID    string               `json:"taskId"`
}

func rawJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func definitionString(d workflowDefinition, key string) string {
	var s string
	_ = json.Unmarshal(d[key], &s)
	return s
}

func (r *workflowResource) definition(ctx context.Context, id string) (workflowDefinition, error) {
	var result workflowDefinitions
	response, err := r.client.Do(ctx, http.MethodPost, "/rest/api/3/workflows", rawJSON(map[string]any{"workflowIds": []string{id}}))
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return nil, fmt.Errorf("reading workflow: HTTP %d: %s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	if result.Workflows == nil {
		return nil, fmt.Errorf("workflow response is missing definitions; refusing to remove state")
	}
	for _, w := range result.Workflows {
		if definitionString(w, "id") == id {
			return w, nil
		}
	}
	return nil, nil
}

func (r *workflowResource) workflowPayload(ctx context.Context, name, description string, ids []string, current workflowDefinition) (map[string]any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("at least one status is required")
	}
	workflow := workflowDefinition{}
	if current != nil {
		// Copy rather than mutate the fetched snapshot.
		for k, v := range current {
			workflow[k] = v
		}
		if len(workflow["version"]) == 0 || string(workflow["version"]) == "null" {
			return nil, fmt.Errorf("workflow response has no version; refusing unsafe update")
		}
		if len(workflow["transitions"]) == 0 || string(workflow["transitions"]) == "null" {
			return nil, fmt.Errorf("workflow response has no transitions; refusing unsafe update")
		}
		delete(workflow, "isEditable")
		delete(workflow, "scope")
		delete(workflow, "name") // Rename is not supported by WorkflowUpdate.
	} else {
		workflow["name"] = rawJSON(name)
		workflow["transitions"] = rawJSON([]any{map[string]any{"id": "1", "name": "Create", "type": "INITIAL", "toStatusReference": ids[0], "links": []any{}, "properties": map[string]string{}, "actions": []any{}, "validators": []any{}, "triggers": []any{}}})
	}
	workflow["description"] = rawJSON(description)
	var oldStatuses []workflowDefinition
	if current != nil {
		if err := json.Unmarshal(current["statuses"], &oldStatuses); err != nil {
			return nil, fmt.Errorf("reading workflow statuses: %w", err)
		}
	}
	layouts := make([]workflowDefinition, 0, len(ids))
	statuses := make([]map[string]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, fmt.Errorf("duplicate status ID %q", id)
		}
		seen[id] = true
		var status struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Category    struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		}
		if err := r.client.Get(ctx, "/rest/api/3/status/"+atlassian.PathEscape(id), &status); err != nil {
			return nil, err
		}
		category := map[string]string{"new": "TODO", "indeterminate": "IN_PROGRESS", "done": "DONE"}[status.Category.Key]
		if category == "" || status.Name == "" {
			return nil, fmt.Errorf("incomplete definition for status %q", id)
		}
		statuses = append(statuses, map[string]any{"id": id, "statusReference": id, "name": status.Name, "description": status.Description, "statusCategory": category})
		layout := workflowDefinition{"statusReference": rawJSON(id), "properties": rawJSON(map[string]string{})}
		for _, old := range oldStatuses {
			if definitionString(old, "statusReference") == id {
				layout = old
				break
			}
		}
		delete(layout, "deprecated")
		layouts = append(layouts, layout)
	}
	// Do not invent migration mappings when removing statuses. Jira validation
	// must reject any removal requiring issue migration or breaking transitions.
	workflow["statuses"] = rawJSON(layouts)
	body := map[string]any{"statuses": statuses, "workflows": []workflowDefinition{workflow}}
	if current == nil {
		body["scope"] = map[string]string{"type": "GLOBAL"}
	}
	return body, nil
}

func (r *workflowResource) mutateWorkflow(ctx context.Context, operation string, payload map[string]any) (*workflowDefinitions, error) {
	endpoint := "/rest/api/3/workflows/" + operation
	var validation struct {
		Errors []struct {
			Level   string `json:"level"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"errors"`
	}
	if err := r.client.Post(ctx, endpoint+"/validation", map[string]any{"payload": payload, "validationOptions": map[string]any{"levels": []string{"ERROR"}}}, &validation); err != nil {
		return nil, err
	}
	for _, e := range validation.Errors {
		if e.Level != "WARNING" {
			return nil, fmt.Errorf("workflow validation failed (%s): %s", e.Code, e.Message)
		}
	}
	var result workflowDefinitions
	if err := r.client.Post(ctx, endpoint, payload, &result); err != nil {
		return nil, err
	}
	if result.TaskID != "" {
		if err := waitWorkflowTask(ctx, r.client, result.TaskID); err != nil {
			return &result, err
		}
	}
	return &result, nil
}

func waitWorkflowTask(ctx context.Context, client *atlassian.Client, id string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	for {
		var task struct {
			Status  string          `json:"status"`
			Message string          `json:"message"`
			Result  json.RawMessage `json:"result"`
		}
		if err := client.Get(ctx, "/rest/api/3/task/"+atlassian.PathEscape(id), &task); err != nil {
			return fmt.Errorf("reading migration task %s: %w", id, err)
		}
		switch task.Status {
		case "COMPLETE":
			return nil
		case "FAILED", "DEAD", "CANCELLED":
			return fmt.Errorf("migration task %s %s: %s %s", id, task.Status, task.Message, task.Result)
		case "ENQUEUED", "RUNNING":
		default:
			return fmt.Errorf("migration task %s returned unknown status %q", id, task.Status)
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("waiting for migration task %s: %w", id, ctx.Err())
		case <-timer.C:
		}
	}
}
