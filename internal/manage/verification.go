package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
)

type verification struct {
	cancel   context.CancelFunc
	ctx      context.Context
	done     chan struct{}
	progress chan verificationProgress
	status   string
	running  bool
}
type verificationProgress struct {
	owner          *verification
	checked, total int
}
type verificationFinished struct {
	owner *verification
	text  string
	err   error
}

func (m *model) verifySelected() tea.Cmd {
	backend, ok := m.backend.(VerificationBackend)
	if !ok || !m.actionReady() || m.current().Local == nil {
		return nil
	}
	if m.verification != nil {
		m.verification.cancel()
	}
	item := *m.current()
	ctx, cancel := context.WithCancel(m.ctx)
	v := &verification{cancel: cancel, ctx: ctx, done: make(chan struct{}), progress: make(chan verificationProgress, 1), status: "Verifying " + item.Title() + " · Esc cancels", running: true}
	m.verification = v
	run := func() tea.Msg {
		defer close(v.done)
		text, err := backend.Verify(ctx, item, func(n, total int) {
			if n%256 == 0 || n == total {
				select {
				case v.progress <- verificationProgress{v, n, total}:
				default:
				}
			}
		})
		return verificationFinished{v, text, err}
	}
	return tea.Batch(run, v.next())
}
func (v *verification) next() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-v.progress:
			return msg
		case <-v.done:
			return nil
		case <-v.ctx.Done():
			return nil
		}
	}
}
func (m *model) verificationUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch value := msg.(type) {
	case verificationProgress:
		if value.owner == m.verification && value.owner.running {
			value.owner.status = fmt.Sprintf("Verifying %d / %d events · Esc cancels", value.checked, value.total)
			return true, value.owner.next()
		}
		return true, nil
	case verificationFinished:
		if value.owner != m.verification {
			return true, nil
		}
		v := m.verification
		v.cancel()
		v.running = false
		v.status = value.text
		if value.err != nil {
			v.status = "Verification failed: " + value.err.Error()
			if errors.Is(value.err, context.Canceled) {
				v.status = "Verification canceled"
			}
		}
		return true, nil
	}
	return false, nil
}
