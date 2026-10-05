package live

import (
	"context"
	"testing"
	"time"
)

func TestDiagnosticMonitorSamplesLocalTransportAndReconnectState(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	api, _ := testSFU(t, ctx, nil)
	conn, err := connect(ctx, api, "11111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	reports := make(chan Diagnostics, 10)
	ticks := make(chan time.Time)
	monitor := &diagnosticMonitor{report: func(d Diagnostics) { reports <- d }}
	monitor.set(conn, "connected", true)
	done := make(chan struct{})
	go func() { defer close(done); monitor.run(ctx, ticks) }()
	defer func() { cancel(); <-done }()
	for {
		select {
		case report := <-reports:
			if report.State != "connected" {
				continue
			}
			if report.ReconnectAttempts != 1 || !report.BufferAvailable || report.ObservedAt.IsZero() {
				t.Fatal("transport diagnostics unavailable", report)
			}
			if report.RateAvailable {
				monitor.set(nil, "paused", false)
				ticks <- time.Now()
				select {
				case paused := <-reports:
					if paused.State != "paused" || paused.BufferAvailable || paused.RateAvailable || paused.ReconnectAttempts != 1 {
						t.Fatal("disconnected diagnostics kept old transport counters", paused)
					}
				case <-ctx.Done():
					t.Fatal("paused diagnostics not reported")
				}
				return
			}
			ticks <- time.Now()
		case <-ctx.Done():
			t.Fatal("rate never became available")
		}
	}
}

func TestDiagnosticRatesAndConnectionReset(t *testing.T) {
	var r diagnosticRate
	a, b := &connection{}, &connection{}
	now := time.Unix(100, 0)
	if _, ok := r.sample(a, now, 100); ok {
		t.Fatal("first rate available")
	}
	if rate, ok := r.sample(a, now.Add(2*time.Second), 2148); !ok || rate != 1024 {
		t.Fatal(rate, ok)
	}
	if _, ok := r.sample(b, now.Add(3*time.Second), 4096); ok {
		t.Fatal("connection change retained rate")
	}
	if _, ok := r.sample(b, now.Add(4*time.Second), 2); ok {
		t.Fatal("counter reset retained rate")
	}
}
func TestDiagnosticQuality(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tt := range []struct {
		rtt       int
		buffer    uint64
		state     string
		age       time.Duration
		available bool
		want      string
	}{{149, 0, "", 0, true, "good"}, {150, 0, "", 0, true, "slow"}, {399, 0, "", 0, true, "slow"}, {400, 0, "", 0, true, "congested"}, {1, 1 << 20, "", 0, true, "good"}, {1, 1<<20 + 1, "", 0, true, "congested"}, {0, 0, "", 0, false, "unavailable"}, {100, 0, "", 5 * time.Second, true, "good"}, {100, 0, "", 5*time.Second + 1, true, "stale"}, {500, 1 << 21, "paused", time.Minute, true, "paused"}, {500, 1 << 21, "reconnecting", time.Minute, true, "reconnecting"}, {500, 1 << 21, "connecting", time.Minute, true, "connecting"}} {
		d := Diagnostics{ObservedAt: now.Add(-tt.age), RTT: time.Duration(tt.rtt) * time.Millisecond, RTTAvailable: tt.available, BufferedBytes: tt.buffer, State: tt.state}
		if got := d.Quality(now); got != tt.want {
			t.Fatal(tt, got)
		}
	}
	m := monitorDiagnostics(t.Context(), nil)
	m.set(nil, "reconnecting", true)
	m.set(nil, "connected", false)
	m.set(nil, "reconnecting", true)
	if m.attempts != 2 {
		t.Fatal(m.attempts)
	}
}
