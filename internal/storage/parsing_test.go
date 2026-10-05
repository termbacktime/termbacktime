package storage

import (
	"testing"
	"time"
)

func TestSizeAgeAndFilterValidation(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {" 42 ", 42}, {"2KiB", 2048}, {"2MiB", 2 << 20}, {"1GiB", 1 << 30}, {"1KB", 1000}, {"1MB", 1000000}, {"1GB", 1000000000}, {"3B", 3},
	} {
		got, err := ParseSize(test.input)
		if err != nil || got != test.want {
			t.Fatal(test.input, got, err)
		}
	}
	for _, input := range []string{"", "-1", "1.5MiB", "999999999999999999999GiB", "9223372036854775807KiB", "1TiB"} {
		if _, err := ParseSize(input); err == nil {
			t.Fatal(input)
		}
	}
	for _, test := range []struct {
		input string
		want  time.Duration
	}{{"0s", 0}, {"2d", 48 * time.Hour}, {"48h", 48 * time.Hour}, {"1m", time.Minute}} {
		got, err := ParseAge(test.input)
		if err != nil || got != test.want {
			t.Fatal(test.input, got, err)
		}
	}
	for _, input := range []string{"-1d", "36501d", "-1s", "broken"} {
		if _, err := ParseAge(input); err == nil {
			t.Fatal(input)
		}
	}
	for _, kind := range []string{"", "all", "recordings", "exports", "queue"} {
		if err := (Filter{Kind: kind}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, filter := range []Filter{{Kind: "other"}, {OlderThan: -1}, {MinSize: -1}} {
		if err := filter.Validate(); err == nil {
			t.Fatal(filter)
		}
	}
}
