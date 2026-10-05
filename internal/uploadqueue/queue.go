package uploadqueue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/termbacktime/termbacktime/internal/config"
	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"golang.org/x/sys/unix"
)

type Job struct {
	Storage       string `json:"storage,omitempty"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	Owner         int64  `json:"owner"`
	Created       int64  `json:"created"`
	State         string `json:"state"`
	Source        string `json:"source"`
	Public        bool   `json:"public"`
	Encrypted     bool   `json:"encrypted"`
	Metadata      bool   `json:"metadata"`
	Endpoint      string `json:"endpoint"`
	Sent          int64  `json:"sent_bytes"`
	Total         int64  `json:"total_bytes"`
	Result        string `json:"result,omitempty"`
	Error         string `json:"error,omitempty"`
	RequestSHA256 string `json:"request_sha256,omitempty"`
}
type Store struct{ Root string }

func (s Store) dir() string           { return filepath.Join(s.Root, "queue") }
func (s Store) path(id string) string { return filepath.Join(s.dir(), id) }
func requestDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 6*recording.MaxBytes {
		return "", fmt.Errorf("invalid private upload request")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, 6*recording.MaxBytes+1))
	if err != nil {
		return "", err
	}
	if n > 6*recording.MaxBytes {
		return "", fmt.Errorf("private upload request exceeds limit")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func (s Store) lock() (*os.File, error) {
	if err := config.EnsurePrivateDir(s.dir()); err != nil {
		return nil, err
	}
	dirfd, err := unix.Open(s.dir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirfd)
	fd, err := unix.Openat(dirfd, "runner.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	var file *os.File
	if err == nil {
		file = os.NewFile(uintptr(fd), "runner.lock")
		st, e := file.Stat()
		if e != nil || !st.Mode().IsRegular() {
			file.Close()
			return nil, fmt.Errorf("invalid queue lock")
		}
	}
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("an upload queue runner is already active")
	}
	return file, nil
}
func unlock(file *os.File) { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); file.Close() }
func (s Store) save(job Job) error {
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	dir := s.path(job.ID)
	file, err := os.CreateTemp(dir, ".job-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(file.Name(), filepath.Join(dir, "job.json")); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (s Store) load(id string) (Job, error) {
	var job Job
	if !recording.ValidID(id) {
		return job, fmt.Errorf("use a full queue job ID")
	}
	file, err := os.Open(filepath.Join(s.path(id), "job.json"))
	if err != nil {
		return job, err
	}
	defer file.Close()
	data, err := recording.ReadBounded(file, 65536)
	if err != nil {
		return job, err
	}
	if json.Unmarshal(data, &job) != nil || (job.Version != 1 && job.Version != 2 && job.Version != 3) || job.ID != id || job.Owner <= 0 {
		return job, fmt.Errorf("invalid queue job")
	}
	if (job.Version == 3 && job.Storage != sharing.Repo) || (job.Version < 3 && job.Storage != "" && job.Storage != sharing.Gist) {
		return job, fmt.Errorf("invalid queue destination")
	}
	return job, nil
}
func (s Store) List() ([]Job, error) {
	jobs := []Job{}
	entries, err := os.ReadDir(s.dir())
	if errors.Is(err, os.ErrNotExist) {
		return jobs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !recording.ValidID(entry.Name()) {
			continue
		}
		job, err := s.load(entry.Name())
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Created < jobs[j].Created })
	return jobs, nil
}
func (s Store) Enqueue(ctx context.Context, client *gh.Client, source, original string, encrypted bool, options gh.UploadOptions) (Job, error) {
	var job Job
	owner, err := client.Identity(ctx)
	if err != nil {
		return job, err
	}
	if encrypted {
		if err = client.CheckEncryption(ctx); err != nil {
			return job, err
		}
	}
	r, err := recording.Load(source)
	if err != nil {
		return job, err
	}
	id := recording.NewID()
	if err = config.EnsurePrivateDir(s.dir()); err != nil {
		return job, err
	}
	dir, err := os.MkdirTemp(s.dir(), ".new-*")
	if err != nil {
		return job, err
	}
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(dir)
		}
	}()
	copy := filepath.Join(dir, "recording.tbt")
	if err = recording.Save(copy, r); err != nil {
		return job, err
	}
	var key string
	if client.Storage == sharing.Repo {
		key, err = client.PrepareRepository(ctx, copy, dir, id, r.Title, encrypted, options)
	} else {
		key, err = gh.PrepareUpload(ctx, copy, dir, id, r.Title, encrypted, options)
	}
	if err != nil {
		return job, err
	}
	// The private receipt is durable before the job becomes runnable.
	if err = recording.Publish(filepath.Join(dir, "key.txt"), func(w io.Writer) error { _, e := io.WriteString(w, key); return e }); err != nil {
		return job, err
	}
	info, err := os.Stat(filepath.Join(dir, "request.json"))
	if err != nil {
		return job, err
	}
	job = Job{Version: 2, ID: id, Owner: owner, Created: time.Now().UnixMilli(), State: "pending", Source: original, Public: options.Public, Encrypted: encrypted, Metadata: options.Metadata != "", Endpoint: client.SiteURL, Total: info.Size()}
	if client.Storage == sharing.Repo {
		job.Version = 3
		job.Storage = sharing.Repo
		job.Public = true
	}
	job.Source, err = filepath.Abs(original)
	if err != nil {
		return Job{}, err
	}
	job.RequestSHA256, err = requestDigest(filepath.Join(dir, "request.json"))
	if err != nil {
		return Job{}, err
	}
	data, _ := json.MarshalIndent(job, "", "  ")
	if err = recording.Publish(filepath.Join(dir, "job.json"), func(w io.Writer) error { _, e := w.Write(data); return e }); err != nil {
		return Job{}, err
	}
	privateFiles := []string{"recording.tbt", "request.json", "key.txt"}
	if options.Metadata != "" && client.Storage != sharing.Repo {
		privateFiles = append(privateFiles, gh.PlaybackFilename)
	}
	for _, name := range privateFiles {
		if err = os.Chmod(filepath.Join(dir, name), 0400); err != nil {
			return Job{}, err
		}
	}
	if err = os.Rename(dir, s.path(id)); err != nil {
		return Job{}, err
	}
	parent, err := os.Open(s.dir())
	if err != nil {
		return Job{}, err
	}
	err = parent.Sync()
	parent.Close()
	if err != nil {
		return Job{}, err
	}
	complete = true
	return job, nil
}
func (s Store) Cancel(id string) error {
	job, err := s.load(id)
	if err != nil {
		return err
	}
	if job.State == "complete" {
		return fmt.Errorf("job is already complete")
	}
	// A separate flag lets the foreground runner cancel an in-flight request without racing its journal.
	if !s.canceled(id) {
		if err = recording.Publish(filepath.Join(s.path(id), "cancel"), func(w io.Writer) error { _, e := io.WriteString(w, "cancel\n"); return e }); err != nil {
			return err
		}
	}
	lock, err := s.lock()
	if err != nil {
		return nil
	}
	defer unlock(lock)
	job, err = s.load(id)
	if err != nil {
		return err
	}
	if job.State == "pending" || job.State == "failed" {
		job.State = "canceled"
		return s.save(job)
	}
	return nil
}
func (s Store) Retry(id string) error {
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	job, err := s.load(id)
	if err != nil {
		return err
	}
	if job.State != "failed" && job.State != "canceled" {
		return fmt.Errorf("only rejected or unsent canceled jobs may be retried; run the queue to reconcile uncertain jobs")
	}
	os.Remove(filepath.Join(s.path(id), "cancel"))
	job.State = "pending"
	job.Error = ""
	job.Sent = 0
	return s.save(job)
}
func (s Store) canceled(id string) bool {
	_, err := os.Stat(filepath.Join(s.path(id), "cancel"))
	return err == nil
}
func (s Store) Run(ctx context.Context, client *gh.Client, report func(Job), only ...string) error {
	lock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock(lock)
	jobs, err := s.List()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if len(only) > 0 && job.ID != only[0] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.Version != 2 && job.Version != 3 {
			job.Error = "unsupported legacy upload job; cancel it and create a v1 recording"
			report(job)
			continue
		}
		if job.State == "complete" || job.State == "canceled" || job.State == "failed" {
			continue
		}
		backend, backendErr := client.Backend(ctx, job.Storage)
		if backendErr != nil {
			job.Error = backendErr.Error()
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			continue
		}
		owner, identityErr := backend.Identity(ctx)
		if identityErr != nil {
			job.Error = identityErr.Error()
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			continue
		}
		if job.Owner != owner {
			job.Error = "authenticated GitHub account does not own this job"
			// Preserve whether a creation request might have been submitted. A wrong
			// account must not turn an unsent job into an ambiguous creation.
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			continue
		}
		sender := *backend
		sender.SiteURL = job.Endpoint
		if job.Storage == sharing.Repo {
			if err := s.runRepository(ctx, &sender, &job, report); err != nil {
				return err
			}
			continue
		}
		var id string
		if job.State == "sending" || job.State == "needs attention" {
			matches, e := sender.Reconcile(ctx, job.ID, job.Owner)
			if e != nil || len(matches) != 1 {
				job.State = "needs attention"
				job.Error = "creation outcome remains unresolved; no creation request was repeated"
				if e != nil {
					job.Error = e.Error()
				}
				if len(matches) > 1 {
					job.Error = "multiple Gists match the job identifier"
				}
				if err = s.save(job); err != nil {
					return err
				}
				report(job)
				continue
			}
			id = matches[0]
		} else {
			if s.canceled(job.ID) {
				job.State = "canceled"
				if err = s.save(job); err != nil {
					return err
				}
				report(job)
				continue
			}
			digest, digestErr := requestDigest(filepath.Join(s.path(job.ID), "request.json"))
			if digestErr != nil || (job.RequestSHA256 != "" && digest != job.RequestSHA256) {
				job.Error = "private upload copy is missing or changed; cancel this job and enqueue again"
				if err = s.save(job); err != nil {
					return err
				}
				report(job)
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if s.canceled(job.ID) {
				job.State = "canceled"
				if err = s.save(job); err != nil {
					return err
				}
				report(job)
				continue
			}
			job.State = "sending"
			job.Error = ""
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			requestCtx, cancel := context.WithCancel(ctx)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				ticker := time.NewTicker(100 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-requestCtx.Done():
						return
					case <-ticker.C:
						if s.canceled(job.ID) {
							cancel()
							return
						}
					}
				}
			}()
			var progressErr error
			id, err = sender.SendPrepared(requestCtx, filepath.Join(s.path(job.ID), "request.json"), func(sent int64) {
				job.Sent = sent
				if e := s.save(job); e != nil {
					progressErr = e
					cancel()
				}
				report(job)
			})
			cancel()
			<-stopped
			if err != nil || progressErr != nil {
				job.State = "needs attention"
				job.Error = "creation request interrupted; run again to reconcile before retrying"
				var httpError *gh.HTTPError
				if errors.As(err, &httpError) && httpError.Status >= 400 && httpError.Status < 500 && httpError.Status != 408 {
					job.State = "failed"
					job.Error = httpError.Error()
				}
				if err = s.save(job); err != nil {
					return err
				}
				report(job)
				continue
			}
		}
		key, err := os.ReadFile(filepath.Join(s.path(job.ID), "key.txt"))
		if err != nil {
			return err
		}
		link := sender.PlaybackLink(id, string(key))
		clear(key)
		lib := library.Library{Root: s.Root}
		if err = lib.SaveQueuedReceipt(job.Source, link, library.Receipt{Public: job.Public, Encrypted: job.Encrypted, Metadata: job.Metadata}); err != nil {
			job.State = "needs attention"
			job.Error = "Gist created; saving the local receipt failed"
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			continue
		}
		job.Result = strings.SplitN(link, "#", 2)[0]
		if err = sender.AddPreparedPlaybackLink(ctx, id, job.Encrypted, job.Metadata, s.path(job.ID)); err != nil {
			// The next run reconciles this existing Gist before retrying its README.
			job.State = "needs attention"
			job.Error = "Gist uploaded, but adding the playback link failed: " + err.Error()
			if err = s.save(job); err != nil {
				return err
			}
			report(job)
			continue
		}
		job.State = "complete"
		job.Error = ""
		job.Sent = job.Total
		if err = s.save(job); err != nil {
			return err
		}
		report(job)
	}
	return nil
}

func (s Store) CleanupLock() (func(), error) {
	file, err := s.lock()
	if err != nil {
		return nil, err
	}
	return func() { unlock(file) }, nil
}
