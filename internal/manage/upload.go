package manage

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/upload"
)

type UploadDraft struct {
	Storage   string
	Recording *recording.Recording
	Fields    recording.MetadataFields
	Findings  int
}
type UploadBackend interface {
	PrepareUpload(context.Context, Item) (UploadDraft, error)
	Upload(context.Context, Item, UploadDraft, review.Result, func(upload.Progress)) (upload.Result, error)
	OpenLink(string) error
}
type uploadPrepared struct {
	epoch int
	draft UploadDraft
	err   error
}
type uploadFinished struct {
	epoch  int
	result upload.Result
	err    error
}
type uploadProgressMsg struct {
	epoch    int
	progress upload.Progress
}
type uploadLinkMsg struct {
	epoch int
	err   error
}
type uploadState struct {
	epoch        int
	item         Item
	draft        UploadDraft
	review       *review.Model
	cancel       context.CancelFunc
	progress     chan upload.Progress
	finished     chan uploadFinished
	link, notice string
}

func (m *model) startUpload(item Item) tea.Cmd {
	backend, ok := m.backend.(UploadBackend)
	if !ok {
		m.status = "Uploads are unavailable"
		return nil
	}
	if item.Local == nil || item.Local.Status != "ready" {
		m.status = "Select a completed local recording to upload"
		return nil
	}
	m.upload.epoch++
	epoch := m.upload.epoch
	ctx, cancel := context.WithCancel(m.ctx)
	m.upload.cancel = cancel
	m.upload.item = item
	m.upload.link = ""
	m.mode = "upload-loading"
	m.status = "Preparing upload review…"
	return func() tea.Msg {
		draft, err := backend.PrepareUpload(ctx, item)
		return uploadPrepared{epoch, draft, err}
	}
}
func (m *model) waitUpload() tea.Cmd {
	epoch, progress, finished := m.upload.epoch, m.upload.progress, m.upload.finished
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
			return nil
		case p := <-progress:
			return uploadProgressMsg{epoch, p}
		case done := <-finished:
			return done
		}
	}
}
func (m *model) uploadUpdate(msg tea.Msg) (bool, tea.Cmd) {
	switch msg := msg.(type) {
	case detailMsg, listMsg, actionMsg:
		return false, nil
	case uploadLinkMsg:
		if msg.epoch == m.upload.epoch && m.mode == "upload-result" {
			m.status = "Link action complete"
			if msg.err != nil {
				m.status = "Link action failed: " + msg.err.Error()
			}
		}
		return true, nil
	case uploadPrepared:
		if msg.epoch != m.upload.epoch {
			return true, nil
		}
		m.upload.cancel()
		m.upload.cancel = nil
		if msg.err != nil {
			m.mode = ""
			m.status = "Upload review failed: " + msg.err.Error()
			return true, nil
		}
		m.upload.draft = msg.draft
		m.upload.review = review.New(msg.draft.Recording, msg.draft.Fields, false, false)
		m.upload.review.Managed = true
		m.upload.review.Storage = msg.draft.Storage
		m.upload.review.Findings = msg.draft.Findings
		m.upload.review.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.mode = "upload-review"
		return true, m.upload.review.Init()
	case review.DoneMsg:
		if m.mode != "upload-review" {
			return true, nil
		}
		m.upload.review = nil
		if msg.Err != nil {
			m.mode = ""
			m.status = "Upload canceled"
			return true, nil
		}
		m.mode = "upload-running"
		m.upload.notice = "Preparing recording…"
		ctx, cancel := context.WithCancel(m.ctx)
		m.upload.cancel = cancel
		m.upload.progress = make(chan upload.Progress, 1)
		m.upload.finished = make(chan uploadFinished, 1)
		epoch, item, draft := m.upload.epoch, m.upload.item, m.upload.draft
		progress, finished := m.upload.progress, m.upload.finished
		backend := m.backend.(UploadBackend)
		// One worker owns the operation. Cancellation waits for its final result,
		// retaining a confirmed link even if Escape races with a response.
		go func() {
			result, err := backend.Upload(ctx, item, draft, msg.Result, func(p upload.Progress) {
				select {
				case progress <- p:
				default:
				}
			})
			finished <- uploadFinished{epoch, result, err}
		}()
		return true, m.waitUpload()
	case uploadProgressMsg:
		if msg.epoch != m.upload.epoch || m.mode != "upload-running" {
			return true, nil
		}
		m.upload.notice = msg.progress.Stage
		if msg.progress.Total > 0 {
			m.upload.notice += fmt.Sprintf(" · %.0f%%", 100*float64(msg.progress.Sent)/float64(msg.progress.Total))
		}
		return true, m.waitUpload()
	case uploadFinished:
		if msg.epoch != m.upload.epoch {
			return true, nil
		}
		m.upload.cancel()
		m.upload.cancel = nil
		m.mode = ""
		m.status = "Upload complete"
		if msg.err != nil {
			m.status = "Upload failed: " + msg.err.Error()
			if errors.Is(msg.err, context.Canceled) {
				m.status = "Upload canceled"
			}
			m.status += ". Check GitHub before retrying if sending had started."
		} else if msg.result.QueueID != "" {
			m.status = "Queued " + msg.result.QueueID + " · J lists jobs · U runs queue"
		}
		if msg.result.Warning != "" {
			m.status += " · " + msg.result.Warning
		}
		if msg.result.Link != "" {
			m.upload.link = msg.result.Link
			m.mode = "upload-result"
		}
		return true, m.refreshBadges()
	}
	if m.mode == "upload-review" {
		_, cmd := m.upload.review.Update(msg)
		return true, cmd
	}
	if m.mode == "upload-running" || m.mode == "upload-loading" {
		if k, ok := msg.(tea.KeyPressMsg); ok && (k.String() == "esc" || k.String() == "ctrl+c" || k.String() == "q") {
			m.upload.cancel()
			if m.mode == "upload-loading" {
				m.upload.epoch++
				m.mode = ""
				m.status = "Upload canceled"
			} else {
				m.upload.notice = "Canceling upload…"
			}
		}
		return true, nil
	}
	if m.mode == "upload-result" {
		if k, ok := msg.(tea.KeyPressMsg); ok {
			switch k.String() {
			case "q", "esc", "enter", "ctrl+c":
				m.mode = ""
				return true, nil
			case "c", "o":
				link := m.upload.link
				epoch := m.upload.epoch
				return true, func() tea.Msg {
					var err error
					if k.String() == "c" {
						err = clipboard.WriteAll(link)
					} else {
						err = m.backend.(UploadBackend).OpenLink(link)
					}
					return uploadLinkMsg{epoch, err}
				}
			}
		}
		// Let action results update the status while retaining the link screen.
		if _, ok := msg.(actionMsg); !ok {
			return true, nil
		}
	}
	return false, nil
}
