// Package jsonkeys reads descriptive JSON field names alongside compact wire keys
package jsonkeys

import (
	"encoding/json"
	"fmt"
)

// Unmarshal accepts either spelling, but refuses objects that specify both
// so different readers cannot select different values for the same field
func Unmarshal(data []byte, target any, aliases map[string]string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("expected a JSON object")
	}
	changed := false
	for name, key := range aliases {
		value, ok := fields[name]
		if !ok {
			continue
		}
		if _, exists := fields[key]; exists {
			return fmt.Errorf("ambiguous JSON fields %q and %q", name, key)
		}
		fields[key] = value
		delete(fields, name)
		changed = true
	}
	if changed {
		var err error
		data, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(data, target)
}
