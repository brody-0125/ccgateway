#!/usr/bin/env bash
set -euo pipefail

log() {
  printf '[verify_ccg] %s\n' "$*"
}

fail() {
  printf '[verify_ccg] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage:
  scripts/verify_ccg.sh [options]

Options:
  --binary <path-or-name>      Binary to verify (default: ccg in PATH).
  --checksums <file>           checksums.txt path for artifact verification.
  --artifact <file>            Artifact to verify. Repeatable.
  --skip-binary                Skip binary verification step.
  -h, --help                   Show this help.

Examples:
  scripts/verify_ccg.sh --binary /usr/local/bin/ccg
  scripts/verify_ccg.sh --checksums ./dist/checksums.txt --artifact ./dist/ccgateway_0.1.0_darwin_arm64.tar.gz --skip-binary
  scripts/verify_ccg.sh --checksums ./dist/checksums.txt --skip-binary
EOF
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

collect_artifacts_from_checksums() {
  local checksums_file="$1"
  local checksums_dir="$2"
  local sum path base candidate
  while read -r sum path _; do
    if [[ -z "${sum:-}" || -z "${path:-}" ]]; then
      continue
    fi
    path="${path#\*}"
    base="$path"
    if [[ "$path" == */* ]]; then
      base="${path##*/}"
    fi
    candidate="${checksums_dir}/${base}"
    if [[ -f "$candidate" ]]; then
      ARTIFACTS+=("$candidate")
    fi
  done < "$checksums_file"
}

verify_binary() {
  local requested="$1"
  local resolved=""

  if [[ "$requested" == */* ]]; then
    resolved="$requested"
  else
    resolved="$(command -v "$requested" || true)"
  fi

  [[ -n "$resolved" ]] || fail "binary not found: ${requested}"
  [[ -x "$resolved" ]] || fail "binary is not executable: ${resolved}"

  "$resolved" help >/dev/null
  log "binary runnable: ${resolved}"
}

verify_artifact() {
  local checksums_file="$1"
  local artifact="$2"
  [[ -f "$artifact" ]] || fail "artifact not found: ${artifact}"

  local base expected actual
  base="$(basename "$artifact")"
  expected="$(checksum_for_artifact "$checksums_file" "$base")"
  [[ -n "$expected" ]] || fail "checksum entry not found for ${base}"
  actual="$(sha256_file "$artifact")"

  if [[ "$expected" != "$actual" ]]; then
    fail "checksum mismatch: ${base}"
  fi
  log "checksum OK: ${base}"
}

BINARY="${CCG_BINARY:-ccg}"
CHECKSUMS=""
SKIP_BINARY=0
ARTIFACTS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)
      BINARY="${2:-}"
      shift 2
      ;;
    --checksums)
      CHECKSUMS="${2:-}"
      shift 2
      ;;
    --artifact)
      ARTIFACTS+=("${2:-}")
      shift 2
      ;;
    --skip-binary)
      SKIP_BINARY=1
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

if [[ -z "$CHECKSUMS" && "${#ARTIFACTS[@]}" -gt 0 ]]; then
  usage
  fail "--artifact requires --checksums"
fi

if [[ -z "$CHECKSUMS" && "$SKIP_BINARY" -eq 1 ]]; then
  usage
  fail "nothing to verify: provide --checksums or remove --skip-binary"
fi

if [[ -n "$CHECKSUMS" ]]; then
  [[ -f "$CHECKSUMS" ]] || fail "checksums file not found: ${CHECKSUMS}"
  if [[ "${#ARTIFACTS[@]}" -eq 0 ]]; then
    checksums_dir="$(cd "$(dirname "$CHECKSUMS")" && pwd)"
    collect_artifacts_from_checksums "$CHECKSUMS" "$checksums_dir"
  fi
  [[ "${#ARTIFACTS[@]}" -gt 0 ]] || fail "no artifacts to verify"
  for artifact in "${ARTIFACTS[@]}"; do
    verify_artifact "$CHECKSUMS" "$artifact"
  done
fi

if [[ "$SKIP_BINARY" -eq 0 ]]; then
  verify_binary "$BINARY"
fi

log "done"
