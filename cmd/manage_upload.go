package cmd

import (
	"context"
	"fmt"

	"github.com/termbacktime/termbacktime/internal/manage"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/upload"
)

type manageBackend struct {
	*manage.Store
	options *options
}

func (b *manageBackend) PrepareUpload(ctx context.Context, item manage.Item) (manage.UploadDraft, error) {
	if item.Local == nil || item.Local.Status != "ready" {
		return manage.UploadDraft{}, fmt.Errorf("select a completed local recording")
	}
	r, err := b.Load(ctx, item)
	if err != nil {
		return manage.UploadDraft{}, err
	}
	fields, err := b.options.metadataDraft(r)
	if err != nil {
		return manage.UploadDraft{}, err
	}
	fillMetadata(ctx, r, item.Path())
	if err = ctx.Err(); err != nil {
		return manage.UploadDraft{}, err
	}
	storage, err := b.options.uploadStorage("")
	if err != nil {
		return manage.UploadDraft{}, err
	}
	return manage.UploadDraft{Storage: storage, Recording: r, Fields: fields, Findings: len(recording.Scan(r))}, nil
}
func (b *manageBackend) Upload(ctx context.Context, item manage.Item, draft manage.UploadDraft, reviewed review.Result, progress func(upload.Progress)) (upload.Result, error) {
	// The reviewed snapshot belongs to this operation, never to the library file.
	r := *draft.Recording
	r.Metadata = r.Metadata.Clone()
	warning, err := b.options.applyMetadataReview(&r, reviewed)
	if err != nil {
		return upload.Result{}, err
	}
	result, err := upload.Run(ctx, b.GitHub, b.Library, upload.Request{Storage: reviewed.Storage, Path: item.Path(), Public: reviewed.Public, Encrypted: reviewed.Encrypted, Queue: reviewed.Queue, Retain: true, Prepare: func(target *recording.Recording) error {
		version := target.Info.UploadCLI
		*target = r
		target.Info.UploadCLI = version
		target.Metadata = r.Metadata.Clone()
		return nil
	}}, progress)
	if warning != "" {
		result.Warning += warning
	}
	return result, err
}
func (b *manageBackend) OpenLink(link string) error { return openURL(link) }
