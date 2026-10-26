package jsm

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

// Contract subset from Atlassian's published OpenAPI, see docs/spec/jsm-form-templates.md.
// Descriptions/examples/formats are omitted. Unknown properties remain allowed.
//
//go:embed form_template_schema.json
var formSchemaJSON []byte

type formSchema struct {
	Ref        string                 `json:"$ref"`
	Type       string                 `json:"type"`
	Required   []string               `json:"required"`
	Properties map[string]*formSchema `json:"properties"`
	Additional *formSchema            `json:"additionalProperties"`
	Items      *formSchema            `json:"items"`
	Enum       []string               `json:"enum"`
}

var formSchemas = func() map[string]*formSchema {
	var schemas map[string]*formSchema
	if err := json.Unmarshal(formSchemaJSON, &schemas); err != nil {
		panic(err)
	}
	return schemas
}()

func resolveFormSchema(s *formSchema) *formSchema {
	if s != nil && s.Ref != "" {
		return formSchemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
	}
	return s
}

type formJSONValidator struct{ schema string }

func (v formJSONValidator) Description(context.Context) string {
	return "Must match the documented Forms " + v.schema + " JSON structure."
}
func (v formJSONValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v formJSONValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value, err := decodeFormJSON(req.ConfigValue.ValueString())
	if err == nil {
		err = validateFormSchema(value, formSchemas[v.schema], v.schema, false)
	}
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Forms JSON", err.Error())
	}
}
func validateFormSchema(value any, schema *formSchema, path string, reading bool) error {
	schema = resolveFormSchema(schema)
	if schema == nil {
		return nil
	}
	wrongType := func() error { return fmt.Errorf("%s must be %s", path, schema.Type) }
	switch schema.Type {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return wrongType()
		}
		required := map[string]bool{}
		for _, key := range schema.Required {
			required[key] = true
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("%s.%s is required", path, key)
			}
		}
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			s := schema.Properties[key]
			if s == nil {
				s = schema.Additional
			}
			// Some optional metadata is API-managed. Unknown properties and nullable
			// optional response values are retained rather than rejected.
			if reading && obj[key] == nil && !required[key] {
				continue
			}
			if err := validateFormSchema(obj[key], s, path+"."+key, reading); err != nil {
				return err
			}
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return wrongType()
		}
		for i, v := range a {
			if err := validateFormSchema(v, schema.Items, fmt.Sprintf("%s[%d]", path, i), reading); err != nil {
				return err
			}
		}
	case "string":
		s, ok := value.(string)
		if !ok {
			return wrongType()
		}
		if !reading && len(schema.Enum) > 0 {
			found := false
			for _, e := range schema.Enum {
				found = found || s == e
			}
			if !found {
				return fmt.Errorf("%s must use a documented value (%s)", path, strings.Join(schema.Enum, ", "))
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return wrongType()
		}
	case "number", "integer":
		n, ok := value.(json.Number)
		if !ok {
			return wrongType()
		}
		if schema.Type == "integer" {
			if integer, ok := new(big.Rat).SetString(string(n)); !ok || !integer.IsInt() || !integer.Num().IsInt64() {
				return wrongType()
			}
		}
	}
	return nil
}

// Ignore only omitted optional schema properties. Collection keys (questions,
// conditions, sections), array elements/order and all configured values remain
// authoritative, so additions/removals and changes still produce drift.
func projectFormJSON(value, configured any, schema *formSchema) any {
	schema = resolveFormSchema(schema)
	if schema == nil {
		return value
	}
	switch obj := value.(type) {
	case map[string]any:
		config, ok := configured.(map[string]any)
		if !ok {
			return value
		}
		required := map[string]bool{}
		for _, k := range schema.Required {
			required[k] = true
		}
		result := map[string]any{}
		for k, v := range obj {
			s, known := schema.Properties[k]
			cv, present := config[k]
			if known && !required[k] && !present {
				continue
			}
			if !known {
				s = schema.Additional
			}
			result[k] = projectFormJSON(v, cv, s)
		}
		return result
	case []any:
		config, ok := configured.([]any)
		if !ok {
			return value
		}
		result := make([]any, len(obj))
		for i, v := range obj {
			var cv any
			if i < len(config) {
				cv = config[i]
			}
			result[i] = projectFormJSON(v, cv, schema.Items)
		}
		return result
	default:
		return value
	}
}

// GET uses FormAnswer.users objects; POST/PUT use FormAnswerRequest.users IDs.
// Expose a reusable design payload from both the resource and the data source.
func requestFormDesign(design map[string]any) {
	questions, _ := design["questions"].(map[string]any)
	for _, raw := range questions {
		question, _ := raw.(map[string]any)
		answer, _ := question["defaultAnswer"].(map[string]any)
		users, ok := answer["users"].([]any)
		if !ok {
			continue
		}
		for i, raw := range users {
			if user, ok := raw.(map[string]any); ok {
				users[i] = user["id"]
			}
		}
	}
}
