# TermBackTime

Record, replay, and share terminal sessions. Recordings stay on your computer until you upload them to GitHub or start an encrypted, read-only live session.

## Install

With Go 1.27.1, or automatic Go toolchain selection:

```sh
go install github.com/termbacktime/termbacktime@latest
```

Add `GOBIN`, or your Go workspace's `bin` directory, to `PATH`. Use `@vX.Y.Z` to select a release. To install a checkout, run `go install .`.

For a prebuilt binary, download the installer from your TermBackTime website and review it:

```sh
export SITE_URL=https://termbackti.me
curl -fsSLO "$SITE_URL/install.sh"
sh install.sh
```

The installer verifies release checksums and installs to `~/.local/bin`. It leaves your shell profile alone. Re-run it to update; `--version vX.Y.Z` selects a tag and `--bin-dir DIRECTORY` changes the destination. Supported platforms are macOS, Linux (including WSL), and FreeBSD. Native Windows is unsupported.

Normal commands check for updates once a day without installing them. Check immediately with `termbacktime --check-update`. Remove the executable to uninstall; your data remains.

## Record

```sh
termbacktime record --title "Debugging a test"
termbacktime record --output demo.tbt -- go test ./...
termbacktime record --dashboard --shell /bin/bash
termbacktime record --upload --no-metadata
```

Interactive recordings load your login-shell setup; type `exit` to finish and upload, even if the last command failed. Single-command recordings preserve exit status and upload only on success. Add `--dashboard` for status outside the recorded output; `Ctrl+]` enters dashboard controls and `Esc` returns to the shell.

Files default to `~/termbacktime/recordings/`. Custom output paths keep their chosen location, create missing parents, and never overwrite files. `--sync-interval 1s` reduces disk syncs, with up to one second potentially lost after a machine crash.

`--upload` shares after capture and normally keeps the local copy. `--no-save` requires `--upload` and deletes its temporary copy even if uploading fails or is canceled; it cannot be combined with `--output` or queueing. `--no-metadata` disables optional computer details.

## Manage recordings

```sh
termbacktime manage
termbacktime list --sort title --order asc
termbacktime list --status ready,partial --pinned=false --uploaded --json
termbacktime list --title demo --after 2026-01-01 --before 2026-12-31
termbacktime info demo.tbt --quick
termbacktime info demo.tbt
termbacktime import /path/to/session.tbt
termbacktime recover /path/to/session.partial --output recovered.tbt
```

Use full library IDs, unambiguous ID prefixes, or paths. Imports are indexed without moving files. Interrupted recordings leave `.partial` journals; recovery preserves the source and refuses active journals. `info --quick` previews metadata; ordinary `info` verifies all chunks.

The manager starts with Local, Gists, or Repository. Press `1/2/3` to switch, `b` for the chooser, arrows or `j/k` to navigate, `Tab` for metadata, and `?` for help.

| Key | Action |
| --- | --- |
| `Enter` / `p` / `v` | Inspect / play / fully verify |
| `/` / `f` | Text filter / structured filters and sorting |
| `u` / `a` | Upload review / saved upload receipts |
| `s` / `r` / `i` | Masked scan / recover / import |
| `n` / `g` | Next GitHub page / refresh |
| `d` / `P` / `S` | Confirm deletion / pin / storage cleanup |
| `J` / `U` / `H` | View queue / run queue / history settings |
| `q` / `Esc` | Quit / return |

GitHub filters cover loaded pages. Local filters include status, pins, and saved uploads; dates are inclusive UTC dates. Badges distinguish ready, recording, partial, missing, unsupported, and invalid files. Deletion requires typing `delete`. Local deletion removes imported files at their original location; remote deletion is separate. Gist deletion removes the whole Gist; repository deletion commits removal, with earlier copies remaining in Git history.

## Review and upload

```sh
termbacktime auth --storage repo --open
termbacktime scan demo.tbt
termbacktime upload demo.tbt
termbacktime auth --storage gist --open
termbacktime upload demo.tbt --storage gist --public
termbacktime upload demo.tbt --encrypt --fail-on-secrets
```

Repository authorization guides you through creating a public `TBT-Recordings` and installing the GitHub App on only that repository. Gists use a separate login. For automation, Gist tokens can be supplied with `TERMBACKTIME_TOKEN` or `auth --token-stdin`. `auth --refresh-client-id` refreshes website discovery; `auth --logout` removes saved logins, optionally for one `--storage`.

Uploads default to Repository or your last confirmed review choice. `--storage repo|gist` overrides one invocation. Gists default to secret and plaintext: anyone with their URL can read them. Repository publications are always public. `--encrypt` protects recording content on either destination; approved README metadata remains readable.

Review the title, description, and computer categories before choosing **Upload now** or **Add to queue**. `--no-metadata` skips review and omits optional metadata. Automation can supply `--description` or `--metadata-file` with a version-1 JSON object, for example `{"version":1,"title":"Demo","description":"Build walkthrough"}`.

Scanning masks findings and warns by default; `--fail-on-secrets` blocks uploads when findings remain. To clean a recording, save `rules.json`:

```json
{"version":1,"detected":true,"literals":["private value"],"intervals":[{"start_ms":12000,"end_ms":18000}]}
```

```sh
termbacktime redact demo.tbt --rules rules.json --output cleaned.tbt
termbacktime play cleaned.tbt
termbacktime upload cleaned.tbt
termbacktime info demo.tbt --show-share-link
```

Redaction creates a new recording and removes hidden links, clipboard requests, and images. Replay it before sharing. Keep encrypted links complete: their fragment carries the key. Saved receipts retain private links; normal listings omit them. In saved uploads, `v` checks availability and `c/o` copies/opens the selected link.

### Upload queue

```sh
termbacktime upload demo.tbt --queue
termbacktime queue list --json
termbacktime queue run
termbacktime queue retry JOB_ID
termbacktime queue cancel JOB_ID
```

Each explicit run processes its initial snapshot; later jobs wait. In the manager queue, `R/C/X` requeue an eligible failed/canceled job, cancel a job, or stop the runner. `Esc` returns to browsing; quitting stops the runner. “Needs attention” jobs are reconciled before another creation attempt.

## Play and export

```sh
termbacktime play demo.tbt --speed 2 --idle-limit 2s
termbacktime play github-user/recording-id
termbacktime export demo.tbt --format txt --output transcript.txt
termbacktime export demo.tbt --format md --output transcript.md
termbacktime history enable
termbacktime play demo.tbt --no-history
termbacktime history disable
termbacktime history clear
```

Playback accepts local files, supported journals, and shared links. `Space` pauses; arrows jump five seconds; `,/.` step events; `+/-` change speed; `I/O` set loop bounds; `L` toggles looping. `q`, `Esc`, or `Ctrl+C` exits. `--no-interactive` disables controls.

History is opt-in and offers **Resume / Start over**. Disabling retains positions; clearing deletes them. Website settings independently offer **Remember playback position**. Browser exports also include snapshots, offline/protected HTML, GIF, and WebM; CLI exports are text and Markdown.

## Live sharing

```sh
termbacktime live
termbacktime live --record --ttl 2h --title "Build walkthrough"
termbacktime live --output live.tbt --no-dashboard -- go test ./...
```

Send the complete viewer link. Viewers cannot control your shell. The separate private host link manages admission, pause, and ending sharing. Pausing or ending sharing leaves the shell and local recording running.

Live sessions default to eight hours within deployment limits, support 100 viewers, and save only when requested. Viewers can rewind up to five minutes received since joining. The dashboard shows connection diagnostics; `Ctrl+]` provides viewer/host link actions, `Esc` returns, and `]` forwards a literal control key.

## Embedding on third-party websites

Open an unencrypted share in the website player and select **Copy iframe embed**. Paste it into a page allowing HTML:

```html
<iframe src="https://termbackti.me/embed/GITHUB_USER/RECORDING_ID?start=12.5&theme=dark"
  title="Terminal recording" width="800" height="450"
  style="width:100%;border:0" loading="lazy" allowfullscreen></iframe>
```

Gists use `/embed/GIST_ID`. Public repository recordings and public or secret Gists work; encrypted recordings do not. Embedding a secret Gist exposes it to visitors. Use the embed route, since ordinary playback pages block framing.

`start` selects original-time seconds. `controls`, `settings`, `details`, and `fullscreen` accept `0/1`. `exports` accepts `0`, `1`, or formats such as `txt,md,tbt`. `font` (10–24), `fit`, `idle` (0/1/2/5), `theme`, `images`, `ligatures`, and `graphemes` customize the player. The website docs include defaults and sandbox guidance.

## GitHub Actions

The composite Action records a command and attaches a cleaned recording only when it fails. This checkout can use:

```yaml
name: Tests
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7.0.1
      - uses: ./termbacktime
        with:
          command: go test ./...
          working-directory: termbacktime
```

In the extracted CLI repository use `./`. Other repositories use `termbacktime/termbacktime@TAG_OR_SHA` once that revision is published, replacing the placeholder. Set up your project's dependencies before this step.

Inputs: required `command`; `shell` (bash), `working-directory` (.), `artifact-name` (terminal-failure), and `retention-days` (7). Outputs: `exit-code`, `artifact-id`, and `artifact-url`. Linux and macOS runners require Go and shell utilities; the Action sets up Go itself.

A failed command keeps its exit status. Download its artifact from the workflow run, then use `termbacktime play terminal-recording.tbt` or the browser player. Preparation/rescan failures publish only `diagnostic.txt`. Successful commands upload nothing. Automatic secret detection is limited; review recordings before sharing them.

## Storage and troubleshooting

```sh
termbacktime storage usage --json
termbacktime storage clean --kind recordings --older-than 30d --min-size 10MiB
termbacktime storage clean --kind exports --apply
termbacktime storage clean --kind queue --apply --yes --json
termbacktime storage pin demo.tbt
termbacktime storage unpin demo.tbt
termbacktime doctor --json
termbacktime doctor --live --relay-only
termbacktime completion bash
```

Cleanup previews first. Applying requires typed confirmation or `--apply --yes` for scripts. Pins, external imports, unfinished journals, unresolved uploads, configuration, history, and receipts are protected.

`--data-dir` or `TERMBACKTIME_DATA_DIR` changes the data root; `--config` independently selects configuration. `--endpoint` or `SITE_URL` selects the website. Local recording/playback supports **1 GiB** expanded data; uploads, scanning, redaction, transcripts, and offline exports support **64 MiB**. Only finalized `.tbt` v1 recordings and supported journals work; old formats are left untouched without conversion.

Run `termbacktime COMMAND --help` for options. Completion supports bash, zsh, fish, and PowerShell. Build, test, and release tooling uses Go. Run `make test`, `make check`, or `make release` (optionally `VERSION=v1.2.3`). Release packaging requires Git and writes nine platform archives plus `SHA256SUMS` under `builds/VERSION/`; it uses the production `SITE_URL` from the shell or `.env` and does not publish.

## License

Copyright (c) 2026 Louis T. Licensed under the MIT License - see [LICENSE](LICENSE).
