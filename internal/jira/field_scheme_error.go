package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// fieldSchemeValidationDetails preserves Jira's structured validation errors,
// including ErrorCollections, without echoing arbitrary proxy/HTML responses.
// Messages can contain tenant-specific configuration, so they belong in the
// operation diagnostic, not in unconditional logs. Bound both input and output.
func fieldSchemeValidationDetails(body io.Reader) string {
	const maxBody = 16 * 1024
	data, err := io.ReadAll(io.LimitReader(body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return ""
	}
	var details []string
	add := func(key, message string) {
		if len(details) >= 4 || message == "" {
			return
		}
		if key != "" {
			message = key + ": " + message
		}
		quoted := fmt.Sprintf("%q", message)
		if len(quoted) > 400 {
			quoted = fmt.Sprintf("%q", message[:64]) + " (truncated)"
		}
		details = append(details, quoted)
	}
	var collect func(json.RawMessage, int)
	collect = func(raw json.RawMessage, depth int) {
		if depth > 4 || len(details) >= 4 {
			return
		}
		var envelope struct {
			ErrorMessages []string        `json:"errorMessages"`
			Errors        json.RawMessage `json:"errors"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			return
		}
		for _, message := range envelope.ErrorMessages {
			add("", message)
		}
		var fields map[string]string
		if json.Unmarshal(envelope.Errors, &fields) == nil {
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				add(key, fields[key])
			}
			return
		}
		var collection []json.RawMessage
		if json.Unmarshal(envelope.Errors, &collection) == nil {
			for _, item := range collection {
				collect(item, depth+1)
			}
		}
	}
	collect(data, 0)
	if len(details) == 0 {
		return ""
	}
	return ": " + strings.Join(details, "; ")
}
