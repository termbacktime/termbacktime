package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/review"
	"github.com/termbacktime/termbacktime/internal/systeminfo"
	"golang.org/x/term"
)

type uploadFlags struct {
	storage                                         string
	storageLocked, rememberStorage                  bool
	encrypt, plain, public, noMetadata, fail, queue bool
	description, metadataFile                       string
}

func (u *uploadFlags) flags(c *cobra.Command) {
	c.Flags().StringVar(&u.storage, "storage", "", "upload storage: repo or gist (last selected, otherwise repo)")
	c.Flags().BoolVar(&u.queue, "queue", false, "save a private upload job for an explicit queue run")
	c.Flags().BoolVar(&u.encrypt, "encrypt", false, "encrypt the recording (README.md remains readable)")
	c.Flags().BoolVar(&u.plain, "no-encrypt", false, "deprecated: plaintext is now the default")
	_ = c.Flags().MarkDeprecated("no-encrypt", "plaintext is now the default; use --encrypt to enable encryption")
	c.Flags().BoolVar(&u.public, "public", false, "create a public Gist (default: secret)")
	c.Flags().BoolVar(&u.noMetadata, "no-metadata", false, "omit optional metadata and skip its review")
	c.Flags().StringVar(&u.description, "description", "", "recording description")
	c.Flags().StringVar(&u.metadataFile, "metadata-file", "", "version 1 metadata JSON with optional title")
	c.Flags().BoolVar(&u.fail, "fail-on-secrets", false, "stop uploads when scanning detects likely secrets")
}
func (u uploadFlags) validate(c *cobra.Command) error {
	if u.storage != "" && u.storage != "repo" && u.storage != "gist" {
		return fmt.Errorf("--storage must be gist or repo")
	}
	if c.Flags().Changed("encrypt") && c.Flags().Changed("no-encrypt") {
		return fmt.Errorf("--encrypt and --no-encrypt cannot be combined")
	}
	if u.noMetadata && (c.Flags().Changed("description") || u.metadataFile != "") {
		return fmt.Errorf("--no-metadata cannot be combined with --description or --metadata-file")
	}
	return nil
}

type metadataDefaults struct {
	Fields      *recording.MetadataFields `json:"fields,omitempty"`
	Title       string                    `json:"title,omitempty"`
	Description string                    `json:"description,omitempty"`
}

func interactiveUpload(c *cobra.Command) bool {
	in, ok := c.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	out, ok := c.OutOrStdout().(*os.File)
	return ok && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))
}
func (o *options) prepareMetadata(c *cobra.Command, r *recording.Recording, path, title string, u uploadFlags) error {
	return o.prepareUploadReview(c, r, path, title, &u)
}
func (o *options) prepareUploadReview(c *cobra.Command, r *recording.Recording, path, title string, u *uploadFlags) error {
	interactive := interactiveUpload(c)
	explicit := c.Flags().Changed("description") || u.metadataFile != ""
	if u.noMetadata || (!interactive && !explicit) {
		r.Metadata = nil
		if title != "" {
			r.Title = title
		}
		return nil
	}
	fields, err := o.metadataDraft(r)
	if err != nil {
		return err
	}
	m := r.Metadata
	// Noninteractive uploads never attach capture hardware merely because it was saved locally.
	if !interactive {
		m = &recording.Metadata{Version: 1}
	}
	if u.metadataFile != "" {
		f, err := os.Open(u.metadataFile)
		if err != nil {
			return err
		}
		defer f.Close()
		b, err := recording.ReadBounded(f, 64<<10)
		if err != nil {
			return err
		}
		var values map[string]json.RawMessage
		if err = json.Unmarshal(b, &values); err != nil {
			return err
		}
		if values == nil {
			return fmt.Errorf("metadata file must contain an object")
		}
		for k := range values {
			if k != "title" && k != "version" && k != "description" && k != "capture_system" && k != "upload_system" {
				return fmt.Errorf("unknown metadata field %q", k)
			}
		}
		if v, ok := values["title"]; ok {
			if err = json.Unmarshal(v, &r.Title); err != nil {
				return err
			}
			delete(values, "title")
		}
		b, _ = json.Marshal(values)
		if err = json.Unmarshal(b, m); err != nil {
			return err
		}
	}
	if title != "" {
		r.Title = title
	}
	if c.Flags().Changed("description") {
		m.Description = u.description
	}
	if err = m.Validate(); err != nil {
		return err
	}
	r.Metadata = m
	if !interactive {
		return nil
	}
	fillMetadata(c.Context(), r, path)
	reviewed, err := review.Run(c.Context(), c.InOrStdin(), c.OutOrStdout(), r, fields, u.public, u.encrypt, review.StorageOptions{Storage: u.storage, Locked: u.storageLocked || c.Flags().Changed("storage")})
	if err != nil {
		return err
	}
	if u.rememberStorage {
		reviewed.RememberStorage = true
	}
	u.storage = reviewed.Storage
	if err := validateRepoVisibility(c, u.storage); err != nil {
		return err
	}
	u.public = reviewed.Public
	u.encrypt = reviewed.Encrypted
	warning, err := o.applyMetadataReview(r, reviewed)
	if warning != "" {
		fmt.Fprintln(c.ErrOrStderr(), warning)
	}
	return err
}

func validateRepoVisibility(c *cobra.Command, storage string) error {
	public, _ := c.Flags().GetBool("public")
	if storage == "repo" && c.Flags().Changed("public") && !public {
		return fmt.Errorf("repository storage is public; use --storage gist for secret uploads")
	}
	return nil
}

func (o *options) metadataDraft(r *recording.Recording) (recording.MetadataFields, error) {
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return recording.MetadataFields{}, err
	}
	var defaults metadataDefaults
	_ = json.Unmarshal(cfg["upload_metadata"], &defaults)
	fields := recording.AllMetadataFields()
	if defaults.Fields != nil {
		fields = *defaults.Fields
	}
	if r.Title == "" {
		r.Title = defaults.Title
	}
	m := &recording.Metadata{Version: 1, Description: defaults.Description}
	if r.Metadata != nil {
		m = r.Metadata.Clone()
		if m.Description == "" {
			m.Description = defaults.Description
		}
	}
	r.Metadata = m
	return fields, nil
}
func fillMetadata(ctx context.Context, r *recording.Recording, path string) {
	m := r.Metadata
	if m.CaptureSystem == nil && m.UploadSystem == nil {
		m.UploadSystem = systeminfo.Collect(ctx, filepath.Dir(path))
	}
	if r.Title == "" {
		r.Title = "Terminal recording - " + time.Unix(r.Started, 0).Format("2006-01-02")
	}
	if m.Description == "" {
		m.Description = fmt.Sprintf("Terminal session recorded on %s (%.1f seconds).", time.Unix(r.Started, 0).Format("2006-01-02"), float64(recording.Duration(r))/1000)
	}
}
func (o *options) applyMetadataReview(r *recording.Recording, reviewed review.Result) (string, error) {
	r.Title = reviewed.Title
	r.Metadata.Description = reviewed.Description
	r.SelectMetadata(reviewed.Fields)
	if err := r.Validate(); err != nil {
		return "", err
	}
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return "Could not save metadata preferences: " + err.Error(), nil
	}
	var defaults metadataDefaults
	_ = json.Unmarshal(cfg["upload_metadata"], &defaults)
	defaults.Fields = &reviewed.Fields
	if reviewed.RememberTemplate {
		defaults.Title = reviewed.Title
		defaults.Description = reviewed.Description
	}
	cfg["upload_metadata"], _ = json.Marshal(defaults)
	if reviewed.RememberStorage && (reviewed.Storage == "repo" || reviewed.Storage == "gist") {
		cfg.Set("upload_storage", reviewed.Storage)
	}
	if err = cfg.Save(o.configPath); err != nil {
		return "Could not save metadata preferences: " + err.Error(), nil
	}
	return "", nil
}
