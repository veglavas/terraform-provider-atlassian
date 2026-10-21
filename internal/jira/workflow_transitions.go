package jira

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var workflowTransitionTypes = map[string]attr.Type{
	"name": types.StringType, "type": types.StringType, "to_status_id": types.StringType,
	"from_status_ids": types.ListType{ElemType: types.StringType},
}

type managedWorkflowTransition struct {
	Name types.String `tfsdk:"name"`
	Type types.String `tfsdk:"type"`
	To   types.String `tfsdk:"to_status_id"`
	From types.List   `tfsdk:"from_status_ids"`
}

func workflowTransitionsSchema() schema.MapNestedAttribute {
	return schema.MapNestedAttribute{
		Optional:    true,
		Description: "Transitions managed by Terraform, keyed by stable numeric transition ID. Omit to preserve all existing transitions. Only listed transitions are managed; removing a previously configured entry deletes that transition. Rules and properties on existing transitions are preserved.",
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"name":            schema.StringAttribute{Required: true},
			"type":            schema.StringAttribute{Required: true, Description: "INITIAL, GLOBAL, or DIRECTED."},
			"to_status_id":    schema.StringAttribute{Required: true},
			"from_status_ids": schema.ListAttribute{Optional: true, ElementType: types.StringType, Description: "Source status IDs for DIRECTED transitions."},
		}},
	}
}

func applyWorkflowTransitions(ctx context.Context, payload map[string]any, planned, previous types.Map) error {
	if planned.IsNull() {
		return nil
	}
	if planned.IsUnknown() {
		return fmt.Errorf("transitions must be known before applying")
	}
	managed := map[string]managedWorkflowTransition{}
	if d := planned.ElementsAs(ctx, &managed, false); d.HasError() {
		return fmt.Errorf("decoding transitions: %s", d)
	}
	oldManaged := map[string]managedWorkflowTransition{}
	if !previous.IsNull() && !previous.IsUnknown() {
		if d := previous.ElementsAs(ctx, &oldManaged, false); d.HasError() {
			return fmt.Errorf("decoding previous transitions: %s", d)
		}
	}
	workflow := payload["workflows"].([]workflowDefinition)[0]
	var existing []workflowDefinition
	if err := json.Unmarshal(workflow["transitions"], &existing); err != nil {
		return err
	}
	result := make([]workflowDefinition, 0, len(existing)+len(managed))
	for _, transition := range existing {
		id := definitionString(transition, "id")
		desired, configured := managed[id]
		if !configured {
			if _, wasManaged := oldManaged[id]; !wasManaged {
				result = append(result, transition)
			}
			continue
		}
		if err := mergeWorkflowTransition(ctx, id, transition, desired); err != nil {
			return err
		}
		result = append(result, transition)
		delete(managed, id)
	}
	for id, desired := range managed {
		transition := workflowDefinition{"id": rawJSON(id), "properties": rawJSON(map[string]string{}), "actions": rawJSON([]any{}), "validators": rawJSON([]any{}), "triggers": rawJSON([]any{})}
		if err := mergeWorkflowTransition(ctx, id, transition, desired); err != nil {
			return err
		}
		result = append(result, transition)
	}
	workflow["transitions"] = rawJSON(result)
	return nil
}

func mergeWorkflowTransition(ctx context.Context, id string, target workflowDefinition, desired managedWorkflowTransition) error {
	if id == "" {
		return fmt.Errorf("transition ID must not be empty")
	}
	kind := desired.Type.ValueString()
	if kind != "INITIAL" && kind != "GLOBAL" && kind != "DIRECTED" {
		return fmt.Errorf("transition %s has unsupported type %q", id, kind)
	}
	var sources []string
	if !desired.From.IsNull() {
		if d := desired.From.ElementsAs(ctx, &sources, false); d.HasError() {
			return fmt.Errorf("invalid source statuses for transition %s", id)
		}
	}
	if (kind == "DIRECTED") != (len(sources) > 0) {
		return fmt.Errorf("transition %s: only DIRECTED transitions require source statuses", id)
	}
	// Retain port/layout metadata on unchanged links.
	var oldLinks []workflowDefinition
	if len(target["links"]) > 0 {
		if err := json.Unmarshal(target["links"], &oldLinks); err != nil {
			return err
		}
	}
	links := make([]workflowDefinition, 0, len(sources))
	for _, source := range sources {
		link := workflowDefinition{"fromStatusReference": rawJSON(source)}
		for _, old := range oldLinks {
			if definitionString(old, "fromStatusReference") == source {
				link = old
				break
			}
		}
		links = append(links, link)
	}
	target["name"] = rawJSON(desired.Name.ValueString())
	target["type"] = rawJSON(kind)
	target["toStatusReference"] = rawJSON(desired.To.ValueString())
	target["links"] = rawJSON(links)
	return nil
}

func refreshWorkflowTransitions(ctx context.Context, configured types.Map, definition workflowDefinition) (types.Map, error) {
	if configured.IsNull() {
		return configured, nil
	}
	managed := map[string]managedWorkflowTransition{}
	if d := configured.ElementsAs(ctx, &managed, false); d.HasError() {
		return configured, fmt.Errorf("decoding managed transitions: %s", d)
	}
	var transitions []workflowDefinition
	if err := json.Unmarshal(definition["transitions"], &transitions); err != nil {
		return configured, err
	}
	found := map[string]managedWorkflowTransition{}
	for _, transition := range transitions {
		id := definitionString(transition, "id")
		previous, ok := managed[id]
		if !ok {
			continue
		}
		var links []struct {
			From string `json:"fromStatusReference"`
		}
		if err := json.Unmarshal(transition["links"], &links); err != nil {
			return configured, err
		}
		sources := make([]string, 0, len(links))
		for _, link := range links {
			sources = append(sources, link.From)
		}
		sources = stableWorkflowIDs(ctx, previous.From, sources)
		from := types.ListNull(types.StringType)
		if len(sources) > 0 || !previous.From.IsNull() {
			from, _ = types.ListValueFrom(ctx, types.StringType, sources)
		}
		found[id] = managedWorkflowTransition{Name: types.StringValue(definitionString(transition, "name")), Type: types.StringValue(definitionString(transition, "type")), To: types.StringValue(definitionString(transition, "toStatusReference")), From: from}
	}
	result, d := types.MapValueFrom(ctx, types.ObjectType{AttrTypes: workflowTransitionTypes}, found)
	if d.HasError() {
		return configured, fmt.Errorf("refreshing transitions: %s", d)
	}
	return result, nil
}
