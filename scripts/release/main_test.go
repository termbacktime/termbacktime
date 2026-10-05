package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReleaseRunnerRejectsExtraArgumentsAndCancellation(t *testing.T) {
	if err := run(t.Context(), []string{"v1.0.0", "extra"}, io.Discard); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, args := range [][]string{nil, {"v1.0.0"}} {
		if err := run(ctx, args, io.Discard); !errors.Is(err, context.Canceled) {
			t.Fatal("runner lost cancellation", err)
		}
	}
}
