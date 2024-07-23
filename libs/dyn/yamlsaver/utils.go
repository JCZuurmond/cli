package yamlsaver

import (
	"fmt"
	"slices"

	"github.com/databricks/cli/libs/dyn"
	"github.com/databricks/cli/libs/dyn/convert"
)

// Converts a struct to map. Skips any nil fields.
// It uses `skipFields` to skip unnecessary fields.
// Uses `order` to define the order of keys in resulting outout
func ConvertToMapValue(strct any, order *Order, skipFields []string, dst map[string]dyn.Value) (dyn.Value, error) {
	ref := dyn.NilValue
	mv, err := convert.FromTyped(strct, ref)
	if err != nil {
		return dyn.InvalidValue, err
	}

	if mv.Kind() != dyn.KindMap {
		return dyn.InvalidValue, fmt.Errorf("expected map, got %s", mv.Kind())
	}

	return skipAndOrder(mv, order, skipFields, dst)
}

func skipAndOrder(mv dyn.Value, order *Order, skipFields []string, dst map[string]dyn.Value) (dyn.Value, error) {
	for _, pair := range mv.MustMap().Pairs() {
		k := pair.Key.MustString()
		v := pair.Value
		if v.Kind() == dyn.KindNil {
			continue
		}

		if slices.Contains(skipFields, k) {
			continue
		}

		// If the value is already defined in destination, it means it was
		// manually set due to custom ordering or other customisation required
		// So we're skipping processing it again
		if _, ok := dst[k]; ok {
			continue
		}

		dst[k] = dyn.NewValue(v.Value(), []dyn.Location{{Line: order.Get(k)}})
	}

	return dyn.V(dst), nil
}
