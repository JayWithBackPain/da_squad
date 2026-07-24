#!/usr/bin/env bash
# Build (and optionally update) Lambda zips for analyze + slack handlers.
#
# Usage:
#   ./deploy.sh [product]
#
# Optional env:
#   GOOS_TARGET / GOARCH_TARGET     default linux / arm64
#   AWS_LAMBDA_ANALYZE_NAME        if set, aws lambda update-function-code
#   AWS_LAMBDA_SLACK_NAME          if set, aws lambda update-function-code
#   AWS_PROFILE / AWS_REGION        passed through to aws cli
#
# Full guide: docs/DEPLOY.md
set -euo pipefail

PRODUCT="${1:-default}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
DIST="${ROOT}/dist"
GOOS_TARGET="${GOOS_TARGET:-linux}"
GOARCH_TARGET="${GOARCH_TARGET:-arm64}"

export PATH="${HOME}/sdk/go1.26.4/bin:${PATH}"

echo "==> building product=${PRODUCT} ${GOOS_TARGET}/${GOARCH_TARGET}"
mkdir -p "${DIST}"

build_one() {
  local name="$1"
  local pkg="$2"
  local outdir="${DIST}/${name}"
  rm -rf "${outdir}"
  mkdir -p "${outdir}"
  echo "--> compile ${name}"
  GOOS="${GOOS_TARGET}" GOARCH="${GOARCH_TARGET}" CGO_ENABLED=0 \
    go build -o "${outdir}/bootstrap" "${ROOT}/${pkg}"
  mkdir -p "${outdir}/config/${PRODUCT}" "${outdir}/queries"
  if [[ -f "${ROOT}/config/${PRODUCT}/config.yaml" ]]; then
    cp "${ROOT}/config/${PRODUCT}/config.yaml" "${outdir}/config/${PRODUCT}/config.yaml"
  else
    cp "${ROOT}/config/${PRODUCT}/config.example.yaml" "${outdir}/config/${PRODUCT}/config.yaml"
    echo "warn: using config.example.yaml; prefer real config.yaml or Lambda env overrides"
  fi
  if [[ -d "${ROOT}/queries/${PRODUCT}" ]]; then
    cp -R "${ROOT}/queries/${PRODUCT}" "${outdir}/queries/${PRODUCT}"
  fi
  (cd "${outdir}" && zip -qr "${DIST}/${name}.zip" bootstrap config queries)
  echo "--> wrote ${DIST}/${name}.zip"
}

upload_one() {
  local zip_name="$1"
  local function_name="$2"
  if [[ -z "${function_name}" ]]; then
    return 0
  fi
  if ! command -v aws >/dev/null 2>&1; then
    echo "error: aws cli not found; cannot update ${function_name}" >&2
    exit 1
  fi
  echo "--> update-function-code ${function_name} <- ${DIST}/${zip_name}.zip"
  aws lambda update-function-code \
    --function-name "${function_name}" \
    --zip-file "fileb://${DIST}/${zip_name}.zip" \
    --output text \
    --query 'FunctionArn'
}

build_one "lambda-analyze" "cmd/lambda-analyze"
build_one "lambda-slack" "cmd/lambda-slack"

upload_one "lambda-analyze" "${AWS_LAMBDA_ANALYZE_NAME:-}"
upload_one "lambda-slack" "${AWS_LAMBDA_SLACK_NAME:-}"

echo "==> done"
if [[ -z "${AWS_LAMBDA_ANALYZE_NAME:-}" && -z "${AWS_LAMBDA_SLACK_NAME:-}" ]]; then
  echo "Zips only (no upload). To update Lambdas:"
  echo "  export AWS_LAMBDA_ANALYZE_NAME=<analyze-fn>"
  echo "  export AWS_LAMBDA_SLACK_NAME=<slack-fn>"
  echo "  ./deploy.sh ${PRODUCT}"
  echo "Or see docs/DEPLOY.md"
else
  echo "Upload requested for configured function names. See docs/DEPLOY.md for verification."
fi
