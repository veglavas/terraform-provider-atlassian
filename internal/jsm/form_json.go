package jsm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// formJSONType implements framework semantic equality: object order, whitespace,
// and equivalent JSON numbers are insignificant; array order remains significant.
// Keep this local to Forms rather than changing unrelated resource string types.
type formJSONType struct{ basetypes.StringType }
type formJSON struct{ basetypes.StringValue }

var _ basetypes.StringValuableWithSemanticEquals = formJSON{}

func (formJSONType) String() string                       { return "jsm.formJSONType" }
func (formJSONType) Equal(other attr.Type) bool           { _, ok := other.(formJSONType); return ok }
func (formJSONType) ValueType(context.Context) attr.Value { return formJSON{} }
func (formJSONType) ValueFromString(_ context.Context, v basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return formJSON{v}, nil
}
func (t formJSONType) ValueFromTerraform(ctx context.Context, v tftypes.Value) (attr.Value, error) {
	raw, err := t.StringType.ValueFromTerraform(ctx, v)
	if err != nil {
		return nil, err
	}
	return formJSON{raw.(basetypes.StringValue)}, nil
}
func (formJSON) Type(context.Context) attr.Type { return formJSONType{} }
func (v formJSON) Equal(other attr.Value) bool {
	o, ok := other.(formJSON)
	return ok && v.StringValue.Equal(o.StringValue)
}
func (v formJSON) StringSemanticEquals(_ context.Context, other basetypes.StringValuable) (bool, diag.Diagnostics) {
	o, ok := other.(formJSON)
	if !ok {
		return false, nil
	}
	a, err := decodeFormJSON(v.ValueString())
	if err != nil {
		return false, nil
	}
	b, err := decodeFormJSON(o.ValueString())
	if err != nil {
		return false, nil
	}
	return reflect.DeepEqual(comparableJSON(a), comparableJSON(b)), nil
}
func comparableJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		n, ok := new(big.Rat).SetString(string(x))
		if ok {
			return struct{ Number string }{n.RatString()}
		}
		return x
	case map[string]any:
		m := map[string]any{}
		for k, v := range x {
			m[k] = comparableJSON(v)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, v := range x {
			a[i] = comparableJSON(v)
		}
		return a
	default:
		return v
	}
}
func decodeFormJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("must be valid JSON")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("must contain exactly one JSON value")
	}
	return v, nil
}
func formJSONValue(v any) formJSON {
	b, _ := json.Marshal(v)
	return formJSON{basetypes.NewStringValue(string(b))}
}
