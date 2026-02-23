#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

VERSION="${1:-}"
if [[ -z "${VERSION}" ]]; then
  if command -v git >/dev/null 2>&1; then
    SHORT_SHA="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || true)"
    if [[ -n "${SHORT_SHA}" ]]; then
      VERSION="dev-${SHORT_SHA}"
    else
      VERSION="dev"
    fi
  else
    VERSION="dev"
  fi
fi

OUT_DIR="${REPO_ROOT}/dist"
mkdir -p "${OUT_DIR}"

TARGETS=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

cd "${REPO_ROOT}"
for target in "${TARGETS[@]}"; do
  read -r GOOS GOARCH <<<"${target}"
  EXT=""
  if [[ "${GOOS}" == "windows" ]]; then
    EXT=".exe"
  fi

  OUT_FILE="${OUT_DIR}/ra2fnt-${GOOS}-${GOARCH}${EXT}"
  echo "building ${OUT_FILE}"
  GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o "${OUT_FILE}" ./src/cmd/ra2fnt
done

echo "done: version=${VERSION}" 
echo "artifacts: ${OUT_DIR}"
