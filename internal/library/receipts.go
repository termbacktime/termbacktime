package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ReceiptSummary deliberately has no private playback link.
type ReceiptSummary struct {
	Remote      sharing.Reference `json:"remote"`
	ID          string            `json:"id"`
	RecordingID string            `json:"recording_id"`
	GistID      string            `json:"gist_id"`
	Created     int64             `json:"created"`
	Public      bool              `json:"public"`
	Encrypted   bool              `json:"encrypted"`
}
type Enrichment struct {
	Known   bool
	Pins    map[string]bool
	Uploads map[string]int
}

func receiptGist(link string) (string, error) {
	ref, err := sharing.Parse(link)
	return ref.String(), err
}
func (r ReceiptSummary) Target() string {
	if r.Remote.Storage != "" {
		return r.Remote.String()
	}
	return r.GistID
}
func (l Library) readReceipt(id string) (Receipt, error) {
	var receipt Receipt
	if !recording.ValidID(id) {
		return receipt, fmt.Errorf("invalid receipt ID")
	}
	dir, err := unix.Open(filepath.Join(l.Root, "shares"), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return receipt, err
	}
	defer unix.Close(dir)
	fd, err := unix.Openat(dir, id+".json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return receipt, err
	}
	file := os.NewFile(uintptr(fd), "receipt")
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return receipt, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16384 {
		return receipt, fmt.Errorf("invalid receipt file")
	}
	data, err := recording.ReadBounded(file, 16384)
	if err != nil {
		return receipt, err
	}
	if json.Unmarshal(data, &receipt) != nil || !recording.ValidID(receipt.RecordingID) || receipt.Created <= 0 {
		return receipt, fmt.Errorf("invalid receipt")
	}
	if _, err = receiptGist(receipt.Link); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}
func (l Library) Receipts(ctx context.Context, recordingID string) ([]ReceiptSummary, []string, error) {
	results := []ReceiptSummary{}
	warnings := []string{}
	files, err := os.ReadDir(filepath.Join(l.Root, "shares"))
	if errors.Is(err, os.ErrNotExist) {
		return results, warnings, nil
	}
	if err != nil {
		return results, warnings, err
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, warnings, err
		}
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		receipt, err := l.readReceipt(id)
		if err != nil {
			warnings = append(warnings, "Unreadable upload receipt "+f.Name())
			continue
		}
		if recordingID != "" && receipt.RecordingID != recordingID {
			continue
		}
		ref, _ := sharing.Parse(receipt.Link)
		gist := ""
		if ref.Storage == sharing.Gist {
			gist = ref.ID
		}
		results = append(results, ReceiptSummary{Remote: ref, ID: id, RecordingID: receipt.RecordingID, GistID: gist, Created: receipt.Created, Public: receipt.Public, Encrypted: receipt.Encrypted})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Created == results[j].Created {
			return results[i].ID < results[j].ID
		}
		return results[i].Created > results[j].Created
	})
	return results, warnings, nil
}
func (l Library) ReceiptLink(id, recordingID string) (string, error) {
	receipt, err := l.readReceipt(id)
	if err != nil {
		return "", err
	}
	if receipt.RecordingID != recordingID {
		return "", fmt.Errorf("receipt association changed")
	}
	return receipt.Link, nil
}
func (l Library) Enrichment(ctx context.Context) (Enrichment, []string, error) {
	result := Enrichment{Known: true, Pins: map[string]bool{}, Uploads: map[string]int{}}
	warnings := []string{}
	pinDir := filepath.Join(l.Root, "pins")
	st, err := os.Lstat(pinDir)
	if err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		err = fmt.Errorf("invalid pins directory")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		result.Known = false
		warnings = append(warnings, "Pin status unavailable")
	} else if err == nil {
		files, e := os.ReadDir(pinDir)
		if e != nil {
			result.Known = false
			warnings = append(warnings, "Pin status unavailable")
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return result, warnings, err
			}
			if strings.HasSuffix(f.Name(), ".json") {
				result.Pins[strings.TrimSuffix(f.Name(), ".json")] = true
			}
		}
	}
	receipts, more, err := l.Receipts(ctx, "")
	if ctx.Err() != nil {
		return result, warnings, ctx.Err()
	}
	if err != nil {
		result.Known = false
		warnings = append(warnings, "Upload receipts unavailable")
	}
	warnings = append(warnings, more...)
	for _, receipt := range receipts {
		result.Uploads[receipt.RecordingID]++
	}
	return result, warnings, nil
}
