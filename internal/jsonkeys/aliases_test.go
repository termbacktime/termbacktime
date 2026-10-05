package jsonkeys

import (
	"reflect"
	"testing"
)

func TestAliasesAndConflictingInputs(t *testing.T) {
	type value struct {
		Name  string
		Count int
	}
	for _, test := range []struct {
		data string
		want value
		bad  bool
	}{
		{`{"Name":"hello","Count":2}`, value{"hello", 2}, false},
		{`{"n":"hello","c":2}`, value{"hello", 2}, false},
		{`{"Name":"hello","c":2,"ignored":true}`, value{"hello", 2}, false},
		{`{}`, value{}, false},
		{`{"Name":"a","n":"a"}`, value{}, true},
		{`{"Count":2,"c":2}`, value{}, true},
		{`null`, value{}, true}, {`[]`, value{}, true}, {"broken", value{}, true},
		{`{"c":"invalid"}`, value{}, true},
	} {
		t.Run(test.data, func(t *testing.T) {
			var wire struct {
				Name  string `json:"n"`
				Count int    `json:"c"`
			}
			err := Unmarshal([]byte(test.data), &wire, map[string]string{"Name": "n", "Count": "c"})
			if (err != nil) != test.bad {
				t.Fatal(err)
			}
			if !test.bad && !reflect.DeepEqual(value{wire.Name, wire.Count}, test.want) {
				t.Fatal(wire)
			}
		})
	}
}
