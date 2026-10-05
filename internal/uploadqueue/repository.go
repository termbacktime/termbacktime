package uploadqueue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/sharing"
)

func (s Store) PublishedLink(id string) (string, error) {
	j, err := s.load(id)
	if err != nil {
		return "", err
	}
	if j.State != "complete" {
		return "", fmt.Errorf("%s", j.Error)
	}
	key, err := os.ReadFile(filepath.Join(s.path(id), "key.txt"))
	if err != nil {
		return "", err
	}
	defer clear(key)
	link := j.Result
	if len(key) > 0 {
		link += "#k=" + string(key)
	}
	return link, nil
}

func (s Store) runRepository(ctx context.Context, c *gh.Client, job *Job, report func(Job)) error {
	path := filepath.Join(s.path(job.ID), "request.json")
	digest, err := requestDigest(path)
	if err != nil || digest != job.RequestSHA256 {
		job.Error = "private upload copy is missing or changed; cancel and enqueue again"
		report(*job)
		return s.save(*job)
	}
	if s.canceled(job.ID) && job.State == "pending" {
		job.State = "canceled"
		report(*job)
		return s.save(*job)
	}
	if err = c.CheckRepositorySupport(ctx); err != nil {
		job.Error = err.Error()
		report(*job)
		return s.save(*job)
	}
	reconcileOnly := s.canceled(job.ID)
	job.State = "sending"
	job.Error = ""
	if err = s.save(*job); err != nil {
		return err
	}
	report(*job)
	requestCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-requestCtx.Done():
				return
			case <-ticker.C:
				if !reconcileOnly && s.canceled(job.ID) {
					cancel()
					return
				}
			}
		}
	}()
	var id string
	if reconcileOnly {
		id, err = c.ReconcileRepository(requestCtx, path)
	} else {
		id, err = c.SendRepository(requestCtx, path, func(n int64) { job.Sent = n; report(*job) })
	}
	cancel()
	<-done
	if reconcileOnly && errors.Is(err, gh.ErrPublicationAbsent) {
		job.State = "canceled"
		job.Error = ""
		report(*job)
		return s.save(*job)
	}
	if err != nil {
		job.State = "needs attention"
		job.Error = "Repository upload requires reconciliation: " + err.Error()
		report(*job)
		return s.save(*job)
	}
	key, err := os.ReadFile(filepath.Join(s.path(job.ID), "key.txt"))
	if err != nil {
		return err
	}
	ref, err := sharing.Parse(id)
	if err != nil {
		return err
	}
	link := ref.Link(c.SiteURL, string(key))
	clear(key)
	if err = (library.Library{Root: s.Root}).SaveQueuedReceipt(job.Source, link, library.Receipt{Public: true, Encrypted: job.Encrypted, Metadata: job.Metadata}); err != nil {
		job.State = "needs attention"
		job.Error = fmt.Sprintf("Published; saving receipt failed: %v", err)
		report(*job)
		return s.save(*job)
	}
	job.Result = strings.SplitN(link, "#", 2)[0]
	job.State = "complete"
	job.Error = ""
	job.Sent = job.Total
	report(*job)
	return s.save(*job)
}
