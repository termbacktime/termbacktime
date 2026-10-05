package live

import (
	"context"
	"math"
	"sync"
	"time"
)

// Diagnostics contains only aggregate measurements, never transport addresses or capabilities.
type Diagnostics struct {
	ObservedAt        time.Time
	State             string
	RTT               time.Duration
	RTTAvailable      bool
	BytesPerSecond    float64
	RateAvailable     bool
	BufferedBytes     uint64
	BufferAvailable   bool
	ReconnectAttempts int
}

func (d Diagnostics) Quality(now time.Time) string {
	if d.State == "connecting" || d.State == "reconnecting" || d.State == "paused" || d.State == "ended" {
		return d.State
	}
	if !d.ObservedAt.IsZero() && now.Sub(d.ObservedAt) > 5*time.Second {
		return "stale"
	}
	if d.BufferedBytes > 1<<20 || d.RTTAvailable && d.RTT >= 400*time.Millisecond {
		return "congested"
	}
	if !d.RTTAvailable {
		return "unavailable"
	}
	if d.RTT >= 150*time.Millisecond {
		return "slow"
	}
	return "good"
}

type diagnosticRate struct {
	identity *connection
	at       time.Time
	bytes    uint64
}

func (r *diagnosticRate) sample(identity *connection, at time.Time, sent uint64) (float64, bool) {
	rate, valid := 0.0, r.identity == identity && !r.at.IsZero() && at.After(r.at) && sent >= r.bytes
	if valid {
		rate = float64(sent-r.bytes) / at.Sub(r.at).Seconds()
	}
	r.identity, r.at, r.bytes = identity, at, sent
	return rate, valid
}

type diagnosticMonitor struct {
	mu         sync.Mutex
	connection *connection
	state      string
	attempts   int
	report     func(Diagnostics)
}

func monitorDiagnostics(ctx context.Context, report func(Diagnostics)) *diagnosticMonitor {
	m := &diagnosticMonitor{report: report, state: "connecting"}
	if report == nil {
		return m
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		m.run(ctx, ticker.C)
	}()
	return m
}

func (m *diagnosticMonitor) run(ctx context.Context, ticks <-chan time.Time) {
	var rate diagnosticRate
	for {
		m.mu.Lock()
		conn, state, attempts := m.connection, m.state, m.attempts
		m.mu.Unlock()
		d := Diagnostics{State: state, ReconnectAttempts: attempts}
		if conn != nil {
			d.BufferedBytes = conn.dc.BufferedAmount()
			d.BufferAvailable = true
			if sctp := conn.pc.SCTP(); sctp != nil && sctp.Transport() != nil && sctp.Transport().ICETransport() != nil {
				if stats, ok := sctp.Transport().ICETransport().GetSelectedCandidatePairStats(); ok {
					d.ObservedAt = time.Now()
					d.BytesPerSecond, d.RateAvailable = rate.sample(conn, d.ObservedAt, stats.BytesSent)
					if stats.ResponsesReceived > 0 && !math.IsNaN(stats.CurrentRoundTripTime) && !math.IsInf(stats.CurrentRoundTripTime, 0) && stats.CurrentRoundTripTime >= 0 {
						d.RTT = time.Duration(stats.CurrentRoundTripTime * float64(time.Second))
						d.RTTAvailable = true
					}
				}
			}
		} else {
			rate = diagnosticRate{}
		}
		m.report(d)
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}

func (m *diagnosticMonitor) set(conn *connection, state string, retry bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connection, m.state = conn, state
	if retry {
		m.attempts++
	}
}
