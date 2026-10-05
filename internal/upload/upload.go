// Package upload shares publication preparation and receipt handling between
// the command line and the recording manager. A request is never retried here.
package upload

import (
	"context"
	"fmt"
	"os"
	"strings"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

type Progress struct {
	Stage       string
	Sent, Total int64
}
type Request struct {
	Storage                                         string
	Review                                          func(*recording.Recording, *Request) error
	Path                                            string
	Public, Encrypted, Queue, Retain, FailOnSecrets bool
	Prepare                                         func(*recording.Recording) error
}
type Result struct{ Link, QueueID, Warning string }

func Run(ctx context.Context, client *gh.Client, lib library.Library, req Request, progress func(Progress)) (Result, error) {
	result := Result{}
	report := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if strings.HasSuffix(req.Path, ".partial") {
		return result, fmt.Errorf("recover the journal before uploading")
	}
	report(Progress{Stage: "Preparing recording"})
	prepare := req.Prepare
	if prepare == nil {
		prepare = func(*recording.Recording) error { return nil }
	}
	source, cleanup, err := gh.UploadSource(req.Path, "", func(r *recording.Recording) error {
		if err := prepare(r); err != nil {
			return err
		}
		if req.Review != nil {
			return req.Review(r, &req)
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	defer cleanup()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	r, err := recording.Load(source)
	if err != nil {
		return result, err
	}
	report(Progress{Stage: "Scanning for secrets"})
	if n := len(recording.Scan(r)); n > 0 {
		result.Warning = fmt.Sprintf("Secret scan: %d masked finding(s); use scan and redact to review.", n)
		if req.FailOnSecrets {
			return result, fmt.Errorf("upload stopped by --fail-on-secrets")
		}
	}
	client, err = client.Backend(ctx, req.Storage)
	if err != nil {
		return result, err
	}
	if req.Storage == sharing.Repo {
		req.Public = true
	}
	settings := gh.UploadOptions{Public: req.Public, Metadata: recording.MetadataMarkdown(r), Progress: func(sent, total int64) { report(Progress{Stage: "Uploading", Sent: sent, Total: total}) }}
	if req.Storage == sharing.Repo {
		root := lib.Root
		if !req.Retain {
			if req.Queue {
				return result, fmt.Errorf("queueing is incompatible with --no-save")
			}
			root, err = os.MkdirTemp("", "termbacktime-publication-*")
			if err != nil {
				return result, err
			}
			defer os.RemoveAll(root)
		}
		store := uploadqueue.Store{Root: root}
		job, e := store.Enqueue(ctx, client, source, req.Path, req.Encrypted, settings)
		if e != nil {
			return result, e
		}
		if req.Queue {
			result.QueueID = job.ID
			return result, nil
		}
		e = store.Run(ctx, client, func(j uploadqueue.Job) {
			report(Progress{Stage: "Publishing repository recording", Sent: j.Sent, Total: j.Total})
		}, job.ID)
		if e != nil {
			return result, e
		}
		result.Link, e = store.PublishedLink(job.ID)
		if e != nil && req.Retain {
			return result, fmt.Errorf("repository upload is incomplete; queue job %s can be reconciled with queue run: %w", job.ID, e)
		}
		return result, e
	}
	if req.Queue {
		if !req.Retain {
			return result, fmt.Errorf("queueing is incompatible with --no-save")
		}
		report(Progress{Stage: "Preparing upload queue"})
		job, err := (uploadqueue.Store{Root: lib.Root}).Enqueue(ctx, client, source, req.Path, req.Encrypted, settings)
		if err == nil {
			result.QueueID = job.ID
		}
		return result, err
	}
	if req.Encrypted {
		report(Progress{Stage: "Preparing encrypted upload"})
		if err = client.CheckEncryption(ctx); err == nil {
			result.Link, err = client.UploadEncrypted(ctx, source, settings)
		}
	} else {
		result.Link, err = client.UploadFile(ctx, source, r.Title, settings)
	}
	if err != nil {
		return result, err
	}
	if req.Retain {
		if err = lib.SaveReceipt(req.Path, result.Link, library.Receipt{Public: req.Public, Encrypted: req.Encrypted, Metadata: r.Metadata != nil}); err != nil {
			result.Warning += "\nUploaded, but could not save the share receipt: " + err.Error()
		}
	}
	if client.SiteURL != "" {
		report(Progress{Stage: "Adding playback link"})
		id, linkErr := gh.GistID(result.Link)
		if linkErr == nil {
			linkErr = client.AddPlaybackLink(ctx, id, req.Encrypted, settings.Metadata)
		}
		if linkErr != nil {
			// Creation already succeeded. Keep the private sharing link and receipt,
			// and never invite a retry that could create a duplicate recording.
			result.Warning = strings.TrimSpace(result.Warning + "\nUploaded, but could not add the playback link to the Gist: " + linkErr.Error())
		}
	}

	return result, nil
}
