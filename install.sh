#!/bin/sh
# Install a verified release without changing Go, Git checkouts or shell profiles

set -eu

version=latest
bin_dir=${HOME:?HOME must be set}/.local/bin
work=
staged=

usage() {
  printf '%s\n' \
    'Usage: install.sh [--version vX.Y.Z] [--bin-dir DIRECTORY]' \
    '' \
    'Installs the newest published release, including prereleases, by default' \
    'For source installation: go install github.com/termbacktime/termbacktime@latest'
}

fail() {
  printf 'termbacktime: %s\n' "$*" >&2
  exit 1
}

# Remove only temporary files created by this invocation
cleanup() {
  if [ -n "$work" ]; then
    rm -rf "$work"
  fi

  if [ -n "$staged" ]; then
    rm -f "$staged"
  fi
}

# Keep explicit release selection separate from the removed Go installer
parse_arguments() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --help | -h)
        usage
        exit 0
        ;;
      --version | --bin-dir)
        [ "$#" -ge 2 ] || fail "Missing value for $1"
        case "$1" in
          --version)
            version=$2
            ;;
          --bin-dir)
            bin_dir=$2
            ;;
        esac
        shift 2
        ;;
      *)
        usage >&2
        fail "Unknown argument: $1. Positional Go versions are no longer supported."
        ;;
    esac
  done
  [ -n "$bin_dir" ] || fail 'Installation directory cannot be empty'
  case "$bin_dir" in
    /*) ;;
    *) bin_dir=$PWD/$bin_dir ;;
  esac
}

# Choose a binary that matches native hardware and userspace bitness
detect_platform() {
  os=$(uname -s)
  machine=$(uname -m)
  case "$os" in
    Darwin) platform=darwin ;;
    Linux) platform=linux ;;
    FreeBSD) platform=freebsd ;;
    *) fail "Unsupported OS $os. Try: go install github.com/termbacktime/termbacktime@latest" ;;
  esac

  case "$machine" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64 | ARM64) arch=arm64 ;;
    i386 | i486 | i586 | i686) arch=386 ;;
    armv7*) arch=armv7 ;;
    armv6*) arch=armv6 ;;
    *) fail "Unsupported architecture $machine. Try: go install github.com/termbacktime/termbacktime@latest" ;;
  esac

  # Prefer native Apple Silicon binaries even when the installer runs under Rosetta
  if [ "$platform" = darwin ] && [ "$(sysctl -n hw.optional.arm64 2> /dev/null || true)" = 1 ]; then
    arch=arm64
  fi

  # A 64-bit kernel can host a 32-bit userspace
  if [ "$platform" = linux ] && [ "$(getconf LONG_BIT 2> /dev/null || true)" = 32 ]; then
    case "$arch" in
      amd64) arch=386 ;;
      arm64) arch=armv7 ;;
    esac
  fi

  case "$platform-$arch" in
    darwin-amd64 | darwin-arm64 | linux-amd64 | linux-386 | linux-arm64 | linux-armv6 | linux-armv7 | freebsd-amd64 | freebsd-386)
      :
      ;;
    *)
      fail "No binary for $platform-$arch. Try: go install github.com/termbacktime/termbacktime@latest"
      ;;
  esac
}

# Require HTTPS downloads and a supported SHA-256 implementation
select_download_tools() {
  if command -v curl > /dev/null 2>&1; then
    fetch() {
      curl --fail --silent --show-error --location \
        --proto '=https' --proto-redir '=https' \
        --connect-timeout 15 --max-time 180 --retry 2 \
        --dump-header "$work/headers" --max-filesize "${3:-134217728}" \
        --output "$2" "$1"
    }
  elif command -v wget > /dev/null 2>&1; then
    fetch() (
      # Bound metadata writes even when wget cannot enforce a response size limit
      if [ -n "${3:-}" ]; then ulimit -f 2048; fi
      wget --https-only --timeout=30 --tries=3 --server-response -q -O "$2" "$1" 2> "$work/headers"
    )
  else
    fail 'Install curl or wget first'
  fi

  if command -v sha256sum > /dev/null 2>&1; then
    checksum() {
      sha256sum "$1" | awk '{print $1}'
    }
  elif command -v shasum > /dev/null 2>&1; then
    checksum() {
      shasum -a 256 "$1" | awk '{print $1}'
    }
  elif command -v sha256 > /dev/null 2>&1; then
    checksum() {
      sha256 -q "$1"
    }
  else
    fail 'A SHA-256 utility (sha256sum, shasum or sha256) is required'
  fi

  for utility in tar awk grep mktemp mkdir chmod mv cat rm wc; do
    command -v "$utility" > /dev/null 2>&1 || fail "Required utility is missing: $utility"
  done
}

# Fetch one release at a time and stop at the first published semantic version
resolve_version() {
  if [ "$version" = latest ]; then
    page=1
    version=
    while [ "$page" -le 20 ]; do
      fetch "https://api.github.com/repos/termbacktime/termbacktime/releases?per_page=1&page=$page" "$work/release.json" 1048576 || fail 'Cannot resolve release from GitHub; retry later or select a published tag with --version vX.Y.Z'
      [ "$(wc -c < "$work/release.json")" -le 1048576 ] || fail 'GitHub release response exceeds 1 MiB'
      LC_ALL=C grep -Eq '^[[:space:]]*\[' "$work/release.json" || fail 'Invalid GitHub release response'
      # Both compact and pretty-printed JSON work without requiring jq
      candidate=$(awk -F '\"' '{for (i = 2; i < NF; i++) if ($i == "tag_name" && $(i + 1) ~ /^[[:space:]]*:[[:space:]]*$/) print $(i + 2)}' "$work/release.json")
      if valid_version "$candidate" && ! LC_ALL=C grep -Eq '"draft"[[:space:]]*:[[:space:]]*true' "$work/release.json"; then
        version=$candidate
        break
      fi
      # Never follow a server-supplied URL; only advance our bounded page number
      awk 'tolower($0) ~ /^[[:space:]]*link:/ && /rel="next"/ {found=1} END {exit !found}' "$work/headers" || break
      page=$((page + 1))
    done
    [ -n "$version" ] || fail 'No published semantic-version release found within 20 pages; select a published tag with --version vX.Y.Z'
  fi
  valid_version "$version" || fail 'Invalid release version'
}

valid_version() {
  case "$1" in
    '' | *[!A-Za-z0-9.+-]*) return 1 ;;
  esac
  [ "${#1}" -le 128 ] || return 1
  printf '%s\n' "$1" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || return 1
  # Numeric prerelease identifiers must not contain leading zeroes
  printf '%s\n' "$1" | awk '{sub(/\+.*/, ""); if (sub(/^v[0-9]+\.[0-9]+\.[0-9]+-/, "")) {n=split($0, ids, "."); for (i=1; i<=n; i++) if (ids[i] ~ /^0[0-9]+$/) exit 1}}'
}

# Download the archive and its checksum manifest from the exact release
download_release() {
  archive=termbacktime_${version}_${platform}_${arch}.tar.gz
  base=https://github.com/termbacktime/termbacktime/releases/download/$version
  fetch "$base/$archive" "$work/release.tar.gz" || fail 'Release download failed'
  fetch "$base/SHA256SUMS" "$work/SHA256SUMS" || fail 'Checksum download failed'
}

# Verify the archive before extracting or executing any downloaded content
verify_release() {
  expected=$(awk -v file="$archive" '$2 == file {print $1}' "$work/SHA256SUMS")
  printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[0-9a-f]{64}$' || fail 'Missing or ambiguous release checksum'
  [ "$(checksum "$work/release.tar.gz")" = "$expected" ] || fail 'Checksum mismatch; existing installation preserved'
  # Release archives contain exactly one regular executable, never links or paths
  [ "$(tar -tzf "$work/release.tar.gz")" = termbacktime ] || fail 'Unexpected archive contents'
  case "$(tar -tvzf "$work/release.tar.gz")" in
    -*) ;;
    *) fail 'Archive binary must be a regular file' ;;
  esac
  tar -xzf "$work/release.tar.gz" -C "$work" termbacktime
  if [ ! -f "$work/termbacktime" ] || [ -L "$work/termbacktime" ]; then
    fail 'Invalid binary'
  fi
  chmod 755 "$work/termbacktime"
  reported=$("$work/termbacktime" --version) || fail 'Downloaded binary cannot run on this system'
  case "$reported" in
    "termbacktime $version revision="*) ;;
    *) fail 'Binary version does not match release' ;;
  esac
}

# Stage on the destination filesystem so replacement is atomic
install_binary() {
  [ ! -L "$bin_dir" ] || fail 'Installation directory must not be a symlink'
  mkdir -p "$bin_dir"
  destination=$bin_dir/termbacktime
  [ ! -L "$destination" ] || fail 'Refusing to replace a symlink'
  if [ -e "$destination" ] && [ ! -f "$destination" ]; then
    fail 'Destination is not a regular file'
  fi

  staged=$(mktemp "$bin_dir/.termbacktime.XXXXXXXX")
  cat "$work/termbacktime" > "$staged"
  chmod 755 "$staged"
  mv -f "$staged" "$destination"
  staged=
}

# Explain PATH changes without rewriting user shell profiles
report_installation() {
  printf 'Installed %s to %s\n' "$version" "$destination"
  case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *) printf 'Add this directory to PATH: %s\n' "$bin_dir" ;;
  esac
  active=$(command -v termbacktime || true)
  if [ -n "$active" ] && [ "$active" != "$destination" ]; then
    printf 'Another installation takes precedence on PATH: %s\n' "$active"
  fi
}

main() {
  parse_arguments "$@"
  detect_platform
  select_download_tools

  work=$(mktemp -d "${TMPDIR:-/tmp}/termbacktime.XXXXXXXX")
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM HUP

  resolve_version
  download_release
  verify_release
  install_binary
  report_installation
}

main "$@"
