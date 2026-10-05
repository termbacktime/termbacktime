package library

import "testing"

func TestFilterValidationAcceptsSupportedChoices(t *testing.T) {
	for _, sort := range []string{"", "date", "title", "duration", "size"} {
		for _, order := range []string{"", "asc", "desc"} {
			if err := (Filter{SortBy: sort, Order: order, Statuses: []string{"ready", "recording", "partial", "missing", "unsupported", "invalid"}}).Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, filter := range []Filter{{SortBy: "unknown"}, {Order: "unknown"}, {Statuses: []string{"unknown"}}} {
		if err := filter.Validate(); err == nil {
			t.Fatal(filter)
		}
	}
}
