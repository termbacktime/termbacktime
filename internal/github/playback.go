package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/termbacktime/termbacktime/internal/config"
	"github.com/termbacktime/termbacktime/internal/recording"
)

const PlaybackFilename = "README.md"

// AddPlaybackLink runs after creation, once GitHub has assigned the Gist ID.
// Preserve the approved metadata when adding the link. Encryption keys never
// belong in Gist contents.
func (c *Client) AddPlaybackLink(ctx context.Context, id string, encrypted bool, metadata string) error {
	return c.updatePlaybackReadme(ctx, id, encrypted, metadata, false)
}

// AddPreparedPlaybackLink uses the approved Markdown saved with the queue job,
// including when retrying after creation. Older v2 jobs used a separate metadata
// file; merge and remove that companion in the same Gist update.
func (c *Client) AddPreparedPlaybackLink(ctx context.Context, id string, encrypted, hasMetadata bool, dir string) error {
	if c.SiteURL == "" {
		return nil
	}
	metadata, legacy := "", false
	if hasMetadata {
		file, err := os.Open(filepath.Join(dir, PlaybackFilename))
		if os.IsNotExist(err) {
			file, err = os.Open(filepath.Join(dir, "metadata.md"))
			legacy = true
		}
		if err != nil {
			return fmt.Errorf("read approved upload metadata: %w", err)
		}
		defer file.Close()
		data, err := recording.ReadBounded(file, recording.MaxManifestBytes)
		if err != nil {
			return fmt.Errorf("read approved upload metadata: %w", err)
		}
		metadata = string(data)
	}
	return c.updatePlaybackReadme(ctx, id, encrypted, metadata, legacy)
}

func (c *Client) updatePlaybackReadme(ctx context.Context, id string, encrypted bool, metadata string, removeLegacy bool) error {
	if c.SiteURL == "" {
		return nil
	}
	origin, err := config.ValidateSiteURL(c.SiteURL)
	if err != nil {
		return err
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid Gist ID for playback link")
	}
	heading, details := "# Terminal recording", strings.TrimSpace(metadata)
	if strings.HasPrefix(details, "# ") {
		heading, details, _ = strings.Cut(details, "\n\n")
	}
	markdown := heading + "\n\n[Play recording](" + origin + "/p/" + id + ")\n"
	if encrypted {
		markdown += "\nThis recording is encrypted. To unlock playback, open the complete private sharing link provided by the uploader. The decryption key is not stored in this Gist.\n"
	}
	if details != "" {
		markdown += "\n" + details + "\n"
	}
	files := map[string]any{PlaybackFilename: map[string]string{"content": markdown}}
	if removeLegacy {
		files["metadata.md"] = nil
	}
	body, err := json.Marshal(map[string]any{
		"files": files,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "https://api.github.com/gists/"+id, bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := c.send(req)
	if err != nil {
		return err
	}
	// The response may echo large recordings; only the success status is needed.
	response.Body.Close()
	return nil
}
