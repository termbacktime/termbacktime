package manage

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/termui"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
	"sort"
	"sync"
	"time"
)

type QueueBackend interface {
	Jobs(context.Context) ([]uploadqueue.Job, error)
	RunQueue(context.Context, func(uploadqueue.Job)) error
	RetryJob(context.Context, string) error
	CancelJob(context.Context, string) error
}

func (s *Store) Jobs(ctx context.Context) ([]uploadqueue.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return (uploadqueue.Store{Root: s.Library.Root}).List()
}
func (s *Store) RunQueue(ctx context.Context, report func(uploadqueue.Job)) error {
	if s.GitHub == nil {
		return fmt.Errorf("authenticate with GitHub first")
	}
	return (uploadqueue.Store{Root: s.Library.Root}).Run(ctx, s.GitHub, report)
}
func (s *Store) RetryJob(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return (uploadqueue.Store{Root: s.Library.Root}).Retry(id)
}
func (s *Store) CancelJob(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return (uploadqueue.Store{Root: s.Library.Root}).Cancel(id)
}

type queueRun struct {
	cancel  context.CancelFunc
	done    chan struct{}
	mu      sync.Mutex
	updates map[string]uploadqueue.Job
	err     error
}

func (r *queueRun) report(job uploadqueue.Job) { r.mu.Lock(); r.updates[job.ID] = job; r.mu.Unlock() }
func (r *queueRun) snapshot() []uploadqueue.Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]uploadqueue.Job, 0, len(r.updates))
	for _, job := range r.updates {
		items = append(items, job)
	}
	return items
}

type queueView struct {
	jobs           []uploadqueue.Job
	cursor, offset int
	status         string
	run            *queueRun
	loading        bool
	loadEpoch      int
	lastRefresh    time.Time
	poll           bool
}
type queueTick struct{ owner *queueView }
type queueLoaded struct {
	owner *queueView
	epoch int
	jobs  []uploadqueue.Job
	err   error
}
type queueFinished struct {
	owner *queueView
	run   *queueRun
}
type queueAction struct {
	owner *queueView
	err   error
	text  string
}

func (m *model) closeQueueRunner() {
	if m.queue != nil && m.queue.run != nil {
		m.queue.run.cancel()
		<-m.queue.run.done
	}
}
func (m *model) queueRefresh() tea.Cmd {
	q := m.queue
	if q.loading {
		return nil
	}
	q.loading = true
	q.loadEpoch++
	epoch := q.loadEpoch
	q.lastRefresh = time.Now()
	backend := m.backend.(QueueBackend)
	return func() tea.Msg { jobs, err := backend.Jobs(m.ctx); return queueLoaded{q, epoch, jobs, err} }
}
func (m *model) queuePoll() tea.Cmd {
	q := m.queue
	if q.poll {
		return nil
	}
	q.poll = true
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return queueTick{q} })
}
func (m *model) openQueue(run bool) tea.Cmd {
	backend, ok := m.backend.(QueueBackend)
	if !ok {
		m.status = "Upload queue unavailable"
		return nil
	}
	if m.queue == nil {
		m.queue = &queueView{status: "Loading upload queue…"}
	}
	q := m.queue
	m.mode = "queue"
	cmds := []tea.Cmd{m.queueRefresh(), m.queuePoll()}
	if run && q.run == nil {
		ctx, cancel := context.WithCancel(m.ctx)
		r := &queueRun{cancel: cancel, done: make(chan struct{}), updates: map[string]uploadqueue.Job{}}
		q.run = r
		q.status = "Running queue"
		go func() { defer close(r.done); r.err = backend.RunQueue(ctx, r.report) }()
		cmds = append(cmds, func() tea.Msg { <-r.done; return queueFinished{q, r} })
	}
	return tea.Batch(cmds...)
}
func (q *queueView) merge(jobs []uploadqueue.Job, replace bool) {
	id := ""
	if len(q.jobs) > q.cursor {
		id = q.jobs[q.cursor].ID
	}
	byID := map[string]uploadqueue.Job{}
	if !replace {
		for _, job := range q.jobs {
			byID[job.ID] = job
		}
	}
	for _, job := range jobs {
		byID[job.ID] = job
	}
	q.jobs = nil
	for _, job := range byID {
		q.jobs = append(q.jobs, job)
	}
	sort.Slice(q.jobs, func(i, j int) bool {
		if q.jobs[i].Created == q.jobs[j].Created {
			return q.jobs[i].ID < q.jobs[j].ID
		}
		return q.jobs[i].Created < q.jobs[j].Created
	})
	q.cursor = min(q.cursor, max(0, len(q.jobs)-1))
	for n, job := range q.jobs {
		if job.ID == id {
			q.cursor = n
		}
	}
}
func (m *model) queueUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case queueTick:
		q := msg.owner
		if q != m.queue {
			return true, nil
		}
		q.poll = false
		if q.run != nil {
			q.merge(q.run.snapshot(), false)
		}
		var refresh tea.Cmd
		if m.mode == "queue" && time.Since(q.lastRefresh) >= time.Second {
			refresh = m.queueRefresh()
		}
		if m.mode == "queue" || q.run != nil {
			return true, tea.Batch(refresh, m.queuePoll())
		}
		return true, nil
	case queueLoaded:
		if msg.owner != m.queue || msg.epoch != m.queue.loadEpoch {
			return true, nil
		}
		q := m.queue
		q.loading = false
		if msg.err != nil {
			q.status = msg.err.Error()
		} else {
			q.merge(msg.jobs, true)
			if q.status == "Loading upload queue…" {
				q.status = "Select a job to view its details"
			}
			if q.run != nil {
				q.merge(q.run.snapshot(), false)
			}
		}
		return true, nil
	case queueFinished:
		if msg.owner != m.queue || msg.run != m.queue.run {
			return true, nil
		}
		q := m.queue
		q.merge(msg.run.snapshot(), false)
		q.run = nil
		q.loadEpoch++
		q.loading = false
		q.status = "Queue run finished"
		if msg.run.err != nil {
			q.status = "Queue stopped: " + msg.run.err.Error()
		}
		return true, tea.Batch(m.queueRefresh(), m.refreshBadges())
	case queueAction:
		if msg.owner != m.queue {
			return true, nil
		}
		m.queue.status = msg.text
		if msg.err != nil {
			m.queue.status = msg.err.Error()
		}
		return true, m.queueRefresh()
	}
	if m.mode != "queue" {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	q := m.queue
	switch key.String() {
	case "esc", "q", "J":
		m.mode = ""
		return true, nil
	case "ctrl+c":
		return true, tea.Quit
	case "up", "k":
		q.cursor = max(0, q.cursor-1)
		q.offset = 0
	case "down", "j":
		q.cursor = min(max(0, len(q.jobs)-1), q.cursor+1)
		q.offset = 0
	case "pgdown":
		q.offset += 5
	case "pgup":
		q.offset = max(0, q.offset-5)
	case "g":
		return true, m.queueRefresh()
	case "U":
		return true, m.openQueue(true)
	case "X":
		if q.run != nil {
			q.run.cancel()
			q.status = "Stopping runner…"
		}
	case "R", "C":
		if len(q.jobs) == 0 {
			return true, nil
		}
		job := q.jobs[q.cursor]
		backend := m.backend.(QueueBackend)
		if key.String() == "R" && job.State != "failed" && job.State != "canceled" {
			q.status = "Run the queue to reconcile uncertain jobs; only failed or canceled jobs can be retried"
			return true, nil
		}
		return true, func() tea.Msg {
			var err error
			text := "Cancellation requested"
			if key.String() == "R" {
				err = backend.RetryJob(m.ctx, job.ID)
				text = "Retry queued; U runs it"
			} else {
				err = backend.CancelJob(m.ctx, job.ID)
			}
			return queueAction{q, err, text}
		}
	}
	return true, nil
}
func (m *model) queueIndicator() string {
	if m.queue != nil && m.queue.run != nil {
		return " · Queue running (J)"
	}
	return ""
}
func (m *model) queueScreen() tea.View {
	q := m.queue
	rows := []string{}
	start := max(0, q.cursor-3)
	for n := start; n < len(q.jobs) && n < start+5; n++ {
		job := q.jobs[n]
		prefix := "  "
		if n == q.cursor {
			prefix = "› "
		}
		rows = append(rows, fmt.Sprintf("%s%s · %s · %d/%d bytes", prefix, job.ID[:12], job.State, job.Sent, job.Total))
	}
	if len(rows) == 0 {
		rows = append(rows, "No upload jobs")
	}
	rows = append(rows, "", q.status, "")
	if len(q.jobs) > q.cursor {
		j := q.jobs[q.cursor]
		details := wrapped(fmt.Sprintf("Job %s\nSource %s\nCreated %s\nState %s\n%s", j.ID, j.Source, time.UnixMilli(j.Created).Format(time.RFC3339), j.State, j.Error), max(1, m.width-4))
		q.offset = min(q.offset, max(0, len(details)-3))
		rows = append(rows, details[q.offset:]...)
	}
	body := pane("Upload queue · ↑↓ selects · PgUp/PgDn details", rows, m.width, max(3, m.height-2), true)
	view := tea.NewView(termui.Fit(body+"\nU Run · R Retry · C Cancel job · X Stop runner · Esc Back", m.width, m.height))
	view.AltScreen = true
	return view
}
