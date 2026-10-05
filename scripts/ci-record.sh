#!/usr/bin/env bash
set -euo pipefail
umask 077
raw=$(mktemp -d "${RUNNER_TEMP:-/tmp}/tbt-raw.XXXXXX")
trap 'rm -rf -- "$raw"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
artifacts=$(mktemp -d "${RUNNER_TEMP:-/tmp}/tbt-artifacts.XXXXXX")
printf 'artifacts=%s\n' "$artifacts" >> "$GITHUB_OUTPUT"
diagnostic() {
    rm -f -- "$artifacts/terminal-recording.tbt"
    printf '%s\n' 'Recording artifacts could not be prepared and rescanned. No recording content was published.' > "$artifacts/diagnostic.txt"
}
if ! (cd -- "$TBT_ACTION_PATH" && go build -o "$raw/termbacktime" .) > "$raw/build.log" 2>&1; then
    diagnostic
    printf 'exit-code=1\n' >> "$GITHUB_OUTPUT"
    exit 0
fi
printf '%s\n' "$TBT_COMMAND" > "$raw/command"
set +e
(
    cd -- "$TBT_DIRECTORY" || exit 1
    "$raw/termbacktime" --data-dir "$raw/library" record --no-metadata --output "$raw/recording.tbt" -- "$TBT_SHELL" "$raw/command"
) > "$raw/output" 2> "$raw/errors"
status=$?
set -e
printf 'exit-code=%s\n' "$status" >> "$GITHUB_OUTPUT"
if [[ "$status" == 0 ]]; then exit 0; fi
printf '%s\n' '{"version":1,"detected":true,"literals":[],"intervals":[]}' > "$raw/rules.json"
printf '[]\n' > "$raw/empty-findings.json"
prepare() {
    "$raw/termbacktime" --data-dir "$raw/library" redact "$raw/recording.tbt" --rules "$raw/rules.json" --output "$artifacts/terminal-recording.tbt" &&
        "$raw/termbacktime" --data-dir "$raw/library" scan "$artifacts/terminal-recording.tbt" > "$raw/scan.json" &&
        cmp -s "$raw/scan.json" "$raw/empty-findings.json"
}
if ! prepare > "$raw/preparation.log" 2>&1; then diagnostic; fi
