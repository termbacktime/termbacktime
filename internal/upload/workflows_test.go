package upload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/termbacktime/termbacktime/internal/github"
	"github.com/termbacktime/termbacktime/internal/library"
	"github.com/termbacktime/termbacktime/internal/recording"
	"github.com/termbacktime/termbacktime/internal/sharing"
	"github.com/termbacktime/termbacktime/internal/uploadqueue"
)

func TestPreparationReviewAndScanFailuresNeverCreateUpload(t *testing.T) {
	for _, scenario := range []string{"cancel", "journal", "prepare", "review", "secrets", "backend", "network", "queue temporary"} {
		t.Run(scenario, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.tbt")
			text := "hello"
			if scenario == "secrets" {
				text = "password=verysecretvalue"
			}
			if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}, Lines: []recording.Event{{Lines: []string{text}}}}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			client := gh.New("token")
			calls := 0
			client.HTTP.Transport = uploadTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("network unavailable") })
			req := Request{Path: path, Retain: true}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch scenario {
			case "cancel":
				cancel()
			case "journal":
				req.Path += ".partial"
			case "prepare":
				req.Prepare = func(*recording.Recording) error { return errors.New("preparation canceled") }
			case "review":
				req.Review = func(*recording.Recording, *Request) error { return context.Canceled }
			case "secrets":
				req.FailOnSecrets = true
			case "backend":
				req.Storage = "unknown"
			case "queue temporary":
				req.Queue = true
				req.Retain = false
			}
			if _, err := Run(ctx, client, library.Library{Root: t.TempDir()}, req, nil); err == nil {
				t.Fatal("failure lost")
			}
			if calls != 0 && scenario != "network" {
				t.Fatal("unexpected external request", calls)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("source changed", err)
			}
		})
	}
}

func TestQueuedUploadsAndStorageSelection(t *testing.T) {
	for _, storage := range []string{sharing.Gist, sharing.Repo} {
		for _, encrypted := range []bool{false, true} {
			t.Run(storage+map[bool]string{false: "/plaintext", true: "/encrypted"}[encrypted], func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "source.tbt")
				if err := recording.Save(path, &recording.Recording{Sizes: []int{80, 24}}); err != nil {
					t.Fatal(err)
				}
				client := gh.New("token")
				client.Storage = storage
				client.AppID = 3
				client.SiteURL = "https://example.com"
				client.HTTP.Transport = uploadTransport(func(req *http.Request) (*http.Response, error) {
					if req.Method != "GET" {
						t.Fatal("queue attempted publication", req.Method)
					}
					switch req.URL.Path {
					case "/user":
						return uploadResponse(200, `{"id":7,"login":"alice","type":"User"}`), nil
					case "/api/v1/config":
						return uploadResponse(200, `{"recordingSources":["gist","repo"],"recordingFormats":["tbt-recording-v1","tbt-encrypted-v1"]}`), nil
					case "/user/installations":
						return uploadResponse(200, `{"total_count":1,"installations":[{"id":2,"app_id":3,"account":{"id":7},"repository_selection":"selected","permissions":{"contents":"write","metadata":"read"}}]}`), nil
					case "/user/installations/2/repositories":
						return uploadResponse(200, `{"total_count":1,"repositories":[{"id":21,"name":"TBT-Recordings","default_branch":"main","owner":{"id":7,"login":"alice"}}]}`), nil
					case "/repos/alice/TBT-Recordings":
						return uploadResponse(200, `{"id":21,"name":"TBT-Recordings","default_branch":"main","owner":{"id":7,"login":"alice"}}`), nil
					default:
						t.Fatal(req.URL)
						return nil, errors.New("unexpected request")
					}
				})
				selected := 0
				rootClient := gh.New("other-token")
				rootClient.Select = func(_ context.Context, choice string) (*gh.Client, error) {
					selected++
					if choice != storage {
						t.Fatal(choice)
					}
					return client, nil
				}
				lib := library.Library{Root: t.TempDir()}
				result, err := Run(t.Context(), rootClient, lib, Request{Path: path, Storage: storage, Queue: true, Retain: true, Encrypted: encrypted}, nil)
				if err != nil || result.QueueID == "" || result.Link != "" || selected != 1 {
					t.Fatal(result, err)
				}
				jobs, err := (uploadqueue.Store{Root: lib.Root}).List()
				if err != nil || len(jobs) != 1 || jobs[0].Encrypted != encrypted || jobs[0].State != "pending" || (storage == sharing.Repo && !jobs[0].Public) {
					t.Fatal(jobs, err)
				}
				data, err := json.Marshal(jobs)
				if err != nil || strings.Contains(string(data), "#k=") {
					t.Fatal(string(data), err)
				}
			})
		}
	}
}
