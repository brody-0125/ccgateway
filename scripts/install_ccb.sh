#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/.." && pwd -P)"

log() {
  printf '[install_ccb] %s\n' "$*"
}

fail() {
  printf '[install_ccb] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage:
  scripts/install_ccb.sh [options]

Options:
  --source                     Build and install from local source (./cmd/ccb).
  --source-dir <path>          Source directory for --source mode (default: repo root).
  --repo <owner/repo>          GitHub repo for release install mode.
  --version <tag|latest>       Release tag (vX.Y.Z) or latest (default: latest).
  --from-dist <path>           Install from local dist directory with archives + checksums.txt.
  --arch <arm64|amd64|auto>    Target architecture (default: auto).
  --install-dir <path>         Binary install dir (default: /usr/local/bin).
  --no-sudo                    Do not attempt sudo when install-dir is not writable.
  -h, --help                   Show this help.

Examples:
  scripts/install_ccb.sh --source --install-dir "$HOME/.local/bin"
  scripts/install_ccb.sh --repo owner/repo --version latest --install-dir "$HOME/.local/bin"
  scripts/install_ccb.sh --from-dist ./dist --version latest --install-dir /usr/local/bin
EOF
}

detect_arch() {
  local machine
  machine="$(uname -m)"
  case "$machine" in
    arm64|aarch64) printf 'arm64\n' ;;
    x86_64|amd64) printf 'amd64\n' ;;
    *) fail "unsupported architecture: ${machine}" ;;
  esac
}

detect_os() {
  local kernel
  kernel="$(uname -s)"
  case "$kernel" in
    Darwin) printf 'darwin\n' ;;
    Linux)  printf 'linux\n' ;;
    *) fail "unsupported OS: ${kernel}" ;;
  esac
}

semver_gt() {
  local a1 a2 a3 apre atag b1 b2 b3 bpre btag
  read -r a1 a2 a3 apre atag <<< "$(semver_parse "$1")" || return 1
  read -r b1 b2 b3 bpre btag <<< "$(semver_parse "$2")" || return 1
  if ((10#$a1 > 10#$b1)); then return 0; fi
  if ((10#$a1 < 10#$b1)); then return 1; fi
  if ((10#$a2 > 10#$b2)); then return 0; fi
  if ((10#$a2 < 10#$b2)); then return 1; fi
  if ((10#$a3 > 10#$b3)); then return 0; fi
  if ((10#$a3 < 10#$b3)); then return 1; fi
  # Stable release (no prerelease) is newer than prerelease for the same core version.
  if ((10#$apre < 10#$bpre)); then return 0; fi
  if ((10#$apre > 10#$bpre)); then return 1; fi
  # Deterministic tie-breaker for two prereleases with same core version.
  if ((10#$apre == 1)) && [[ "$atag" > "$btag" ]]; then return 0; fi
  return 1
}

semver_parse() {
  local raw no_build core pre_tag pre_flag
  raw="${1#v}"
  no_build="${raw%%+*}"
  core="${no_build%%-*}"
  pre_tag=""
  pre_flag=0
  if [[ "$no_build" == *-* ]]; then
    pre_flag=1
    pre_tag="${no_build#*-}"
  fi
  local major minor patch
  IFS='.' read -r major minor patch <<< "$core"
  [[ "${major:-}" =~ ^[0-9]+$ ]] || return 1
  [[ "${minor:-}" =~ ^[0-9]+$ ]] || return 1
  [[ "${patch:-}" =~ ^[0-9]+$ ]] || return 1
  printf '%s %s %s %s %s\n' "$major" "$minor" "$patch" "$pre_flag" "$pre_tag"
}

select_latest_dist_archive() {
  local dist_dir="$1"
  local arch="$2"
  local os_name="$3"
  local best_path=""
  local best_ver=""
  local file base ver

  while IFS= read -r file; do
    base="$(basename "$file")"
    if [[ "$base" =~ ^ccgateway_([^_]+)_${os_name}_${arch}\.tar\.gz$ ]]; then
      ver="${BASH_REMATCH[1]}"
      if ! semver_parse "$ver" >/dev/null; then
        continue
      fi
      if [[ -z "$best_ver" ]] || semver_gt "$ver" "$best_ver"; then
        best_ver="$ver"
        best_path="$file"
      fi
    fi
  done < <(find "$dist_dir" -maxdepth 1 -type f -name "ccgateway_*_${os_name}_${arch}.tar.gz" | sort)

  if [[ -n "$best_path" ]]; then
    printf '%s\n' "$best_path"
  fi
}

sha256_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

checksum_for_artifact() {
  local checksums_file="$1"
  local artifact_name="$2"
  awk -v target="$artifact_name" '
    {
      sum=$1
      path=$2
      if (sum == "" || path == "") next
      gsub(/^\*/, "", path)
      n=split(path, parts, "/")
      base=parts[n]
      if (base == target) {
        print sum
        exit
      }
    }
  ' "$checksums_file"
}

install_binary() {
  local src="$1"
  local install_dir="$2"
  local target="${install_dir}/ccb"

  if [[ ! -d "$install_dir" ]]; then
    if mkdir -p "$install_dir" 2>/dev/null; then
      :
    else
      if [[ "$NO_SUDO" -eq 1 ]]; then
        fail "install dir is not writable and cannot be created without sudo: ${install_dir}"
      fi
      if ! command -v sudo >/dev/null 2>&1; then
        fail "install dir cannot be created and sudo is unavailable: ${install_dir}"
      fi
      sudo mkdir -p "$install_dir"
    fi
  fi

  if [[ -w "$install_dir" ]]; then
    install -m 0755 "$src" "$target"
  else
    if [[ "$NO_SUDO" -eq 1 ]]; then
      fail "install dir is not writable: ${install_dir}"
    fi
    if ! command -v sudo >/dev/null 2>&1; then
      fail "install dir not writable and sudo is unavailable: ${install_dir}"
    fi
    sudo install -m 0755 "$src" "$target"
  fi

  log "installed: ${target}"
}

resolve_tag() {
  local repo="$1"
  local version_input="$2"

  if [[ "$version_input" != "latest" ]]; then
    if [[ "$version_input" == v* ]]; then
      printf '%s\n' "$version_input"
    else
      printf 'v%s\n' "$version_input"
    fi
    return
  fi

  local api_url tag
  api_url="https://api.github.com/repos/${repo}/releases/latest"
  tag="$(curl -fsSL "$api_url" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  [[ -n "$tag" ]] || fail "failed to resolve latest release tag from ${api_url}"
  printf '%s\n' "$tag"
}

MODE="release"
SOURCE_DIR="$REPO_ROOT"
REPO="${CCB_GITHUB_REPO:-}"
VERSION="latest"
FROM_DIST=""
INSTALL_DIR="${CCB_INSTALL_DIR:-/usr/local/bin}"
ARCH="auto"
NO_SUDO=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --source)
      MODE="source"
      shift
      ;;
    --source-dir)
      SOURCE_DIR="${2:-}"
      MODE="source"
      shift 2
      ;;
    --repo)
      REPO="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --from-dist)
      FROM_DIST="${2:-}"
      MODE="dist"
      shift 2
      ;;
    --install-dir)
      INSTALL_DIR="${2:-}"
      shift 2
      ;;
    --arch)
      ARCH="${2:-}"
      shift 2
      ;;
    --no-sudo)
      NO_SUDO=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

_DETECTED_OS="$(uname -s)"
case "$_DETECTED_OS" in
  Darwin|Linux) ;;
  *) fail "unsupported OS: ${_DETECTED_OS} (supported: macOS, Linux)" ;;
esac

DETECTED_OS="$(detect_os)"

case "$ARCH" in
  auto) ARCH="$(detect_arch)" ;;
  arm64|amd64) ;;
  *) fail "--arch must be one of: auto, arm64, amd64" ;;
esac

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

if [[ "$MODE" == "release" && -z "$REPO" ]]; then
  usage
  fail "--repo <owner/repo> is required for release install (or use --source / --from-dist)"
fi

case "$MODE" in
  source)
    command -v go >/dev/null 2>&1 || fail "go command not found"
    [[ -f "${SOURCE_DIR}/go.mod" ]] || fail "go.mod not found in source dir: ${SOURCE_DIR} (set --source-dir <path>)"
    [[ -d "${SOURCE_DIR}/cmd/ccb" ]] || fail "cmd/ccb not found in source dir: ${SOURCE_DIR} (set --source-dir <path>)"
    log "building from source: ${SOURCE_DIR}"
    (
      cd "$SOURCE_DIR"
      go build -trimpath -ldflags="-s -w" -o "${TMP_DIR}/ccb" ./cmd/ccb
    )
    install_binary "${TMP_DIR}/ccb" "$INSTALL_DIR"
    ;;
  dist)
    [[ -n "$FROM_DIST" ]] || fail "--from-dist path is required in dist mode"
    [[ -d "$FROM_DIST" ]] || fail "dist directory not found: ${FROM_DIST}"
    CHECKSUMS_FILE="${FROM_DIST}/checksums.txt"
    [[ -f "$CHECKSUMS_FILE" ]] || fail "checksums file not found: ${CHECKSUMS_FILE}"

    if [[ "$VERSION" == "latest" ]]; then
      ARCHIVE_PATH="$(select_latest_dist_archive "$FROM_DIST" "$ARCH" "$DETECTED_OS")"
      [[ -n "$ARCHIVE_PATH" ]] || fail "no archive found for ${DETECTED_OS}_${ARCH} in ${FROM_DIST}"
    else
      TAG="$(resolve_tag "local/local" "$VERSION")"
      VERSION_NO_V="${TAG#v}"
      ARCHIVE_PATH="${FROM_DIST}/ccgateway_${VERSION_NO_V}_${DETECTED_OS}_${ARCH}.tar.gz"
      [[ -f "$ARCHIVE_PATH" ]] || fail "archive not found: ${ARCHIVE_PATH}"
    fi

    ARCHIVE_NAME="$(basename "$ARCHIVE_PATH")"
    EXPECTED_SHA="$(checksum_for_artifact "$CHECKSUMS_FILE" "$ARCHIVE_NAME")"
    [[ -n "$EXPECTED_SHA" ]] || fail "checksum not found for ${ARCHIVE_NAME} in ${CHECKSUMS_FILE}"
    ACTUAL_SHA="$(sha256_file "$ARCHIVE_PATH")"
    [[ "$EXPECTED_SHA" == "$ACTUAL_SHA" ]] || fail "checksum mismatch for ${ARCHIVE_NAME}"

    tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"
    [[ -x "${TMP_DIR}/ccb" ]] || fail "archive does not contain executable ccb"
    install_binary "${TMP_DIR}/ccb" "$INSTALL_DIR"
    ;;
  release)
    command -v curl >/dev/null 2>&1 || fail "curl command not found"
    [[ -n "$REPO" ]] || fail "--repo <owner/repo> is required for release install"

    TAG="$(resolve_tag "$REPO" "$VERSION")"
    VERSION_NO_V="${TAG#v}"
    ARCHIVE_NAME="ccgateway_${VERSION_NO_V}_${DETECTED_OS}_${ARCH}.tar.gz"
    CHECKSUMS_NAME="checksums.txt"
    BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"

    ARCHIVE_PATH="${TMP_DIR}/${ARCHIVE_NAME}"
    CHECKSUMS_PATH="${TMP_DIR}/${CHECKSUMS_NAME}"

    log "downloading ${ARCHIVE_NAME} (${TAG})"
    curl -fL --retry 3 --retry-delay 2 -o "$ARCHIVE_PATH" "${BASE_URL}/${ARCHIVE_NAME}"
    curl -fL --retry 3 --retry-delay 2 -o "$CHECKSUMS_PATH" "${BASE_URL}/${CHECKSUMS_NAME}"

    EXPECTED_SHA="$(checksum_for_artifact "$CHECKSUMS_PATH" "$ARCHIVE_NAME")"
    [[ -n "$EXPECTED_SHA" ]] || fail "checksum not found for ${ARCHIVE_NAME}"
    ACTUAL_SHA="$(sha256_file "$ARCHIVE_PATH")"
    [[ "$EXPECTED_SHA" == "$ACTUAL_SHA" ]] || fail "checksum mismatch for ${ARCHIVE_NAME}"

    tar -xzf "$ARCHIVE_PATH" -C "$TMP_DIR"
    [[ -x "${TMP_DIR}/ccb" ]] || fail "archive does not contain executable ccb"
    install_binary "${TMP_DIR}/ccb" "$INSTALL_DIR"
    ;;
  *)
    fail "unsupported mode: ${MODE}"
    ;;
esac

log "done"
log "run: scripts/verify_ccb.sh --binary ${INSTALL_DIR}/ccb"
