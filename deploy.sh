#!/usr/bin/env bash
#
# deploy.sh — 依 config/<product>/config.yaml 的 deploy 區塊，打包並建立/更新兩個 Lambda。
#
# 用法:
#   ./deploy.sh <product>
#
# 這個專案有「兩個」Lambda，各自獨立打包、各自建立/更新：
#   analyze → cmd/lambda-analyze （EventBridge 每日觸發）
#   slack   → cmd/lambda-slack   （Function URL / API Gateway 收 Slack 互動）
#
# 部署參數全部讀自 config.yaml 的 deploy 區塊（用 yq 解析），不靠環境變數。
# 完整步驟與觸發綁定見 docs/DEPLOY.md
set -euo pipefail

usage() {
  echo "用法: $0 <product>"
  if [[ -d config ]]; then
    echo "可用產品: $(ls config 2>/dev/null | tr '\n' ' ')"
  fi
  exit 1
}

PRODUCT="${1:-}"
[[ -z "${PRODUCT}" ]] && usage

CONFIG_FILE="config/${PRODUCT}/config.yaml"
QUERIES_DIR="queries/${PRODUCT}"

[[ -f "${CONFIG_FILE}" ]] || { echo "[deploy] 找不到 ${CONFIG_FILE}"; usage; }
[[ -d "${QUERIES_DIR}" ]] || { echo "[deploy] 找不到 ${QUERIES_DIR}"; exit 1; }

command -v yq >/dev/null 2>&1 || { echo "[deploy] 需要 yq 來解析 YAML，請先安裝 (brew install yq)"; exit 1; }

# 系統找不到 go 時，補上本機 SDK 路徑（沒有也不強制）
if ! command -v go >/dev/null 2>&1 && [[ -d "${HOME}/sdk/go1.26.4/bin" ]]; then
  export PATH="${HOME}/sdk/go1.26.4/bin:${PATH}"
fi
command -v go >/dev/null 2>&1 || { echo "[deploy] 找不到 go，請先安裝 Go 或設好 PATH"; exit 1; }

# 讀 yaml：把 yq 回的字面 "null" 當成空字串
read_yaml() {
  local val
  val=$(yq -r "$1" "${CONFIG_FILE}")
  [[ "${val}" == "null" ]] && val=""
  echo "${val}"
}

# ── 共用 deploy 設定（兩支函數共用）──
REGION=$(read_yaml '.deploy.region')
AWS_PROFILE=$(read_yaml '.deploy.aws_profile')
LAMBDA_ROLE=$(read_yaml '.deploy.lambda_role')
ARCHITECTURE=$(read_yaml '.deploy.architecture')
[[ -z "${ARCHITECTURE}" ]] && ARCHITECTURE="arm64"
[[ -z "${LAMBDA_ROLE}"  ]] && { echo "[deploy] deploy.lambda_role 為空（Lambda 執行角色 ARN）"; exit 1; }

# AWS 架構名 → Go GOARCH（arm64→arm64、x86_64→amd64）
case "${ARCHITECTURE}" in
  arm64)  GOARCH_TARGET="arm64" ;;
  x86_64) GOARCH_TARGET="amd64" ;;
  *) echo "[deploy] 不支援的 architecture: ${ARCHITECTURE}（請填 arm64 或 x86_64）"; exit 1 ;;
esac

log() { echo "[deploy:${PRODUCT}] $1"; }

# 包一層 aws，統一帶上 profile / region（yaml 有填才帶）
aws_cmd() {
  local args=()
  [[ -n "${AWS_PROFILE}" ]] && args+=(--profile "${AWS_PROFILE}")
  [[ -n "${REGION}"      ]] && args+=(--region  "${REGION}")
  aws "${args[@]}" "$@"
}

# deploy_target <邏輯名> <cmd 套件> <函數名> <timeout> <memory>
#   編譯 → 打包（含 config/queries）→ 不存在就 create、存在就 update-code
deploy_target() {
  local target="$1" pkg="$2" fn="$3" timeout="$4" memory="$5"
  local zip="deployed_${PRODUCT}_${target}.zip"

  [[ -z "${fn}" ]] && { echo "[deploy] deploy.${target}.function_name 為空"; exit 1; }
  [[ -z "${timeout}" ]] && timeout=900
  [[ -z "${memory}"  ]] && memory=1024

  log "編譯 ${target}（GOOS=linux GOARCH=${GOARCH_TARGET}）..."
  # bootstrap＝provided.al2023 規定的入口檔名；lambda.norpc 去掉用不到的 rpc，縮小體積
  GOOS=linux GOARCH="${GOARCH_TARGET}" CGO_ENABLED=0 \
    go build -tags lambda.norpc -o bootstrap "./${pkg}"

  log "打包 ${zip}..."
  rm -f "${zip}"
  zip -r "${zip}" bootstrap "config/${PRODUCT}" "queries/${PRODUCT}" >/dev/null
  ls -lh "${zip}"

  if ! aws_cmd lambda get-function --function-name "${fn}" >/dev/null 2>&1; then
    log "函數不存在 → 建立 ${fn}"
    aws_cmd lambda create-function \
      --function-name "${fn}" \
      --runtime provided.al2023 \
      --handler bootstrap \
      --architectures "${ARCHITECTURE}" \
      --role "${LAMBDA_ROLE}" \
      --zip-file "fileb://${zip}" \
      --timeout "${timeout}" \
      --memory-size "${memory}" \
      --environment "Variables={DA_AGENT_PRODUCT=${PRODUCT},DEPLOY_TIME=$(date +%s)}"
    log "※ 新函數只帶了 DA_AGENT_PRODUCT；機密（REDSHIFT_CONN_STR / GEMINI_API_KEY / SLACK_*）請到 Console 或 CLI 補上再測"
  else
    log "函數已存在 → update-function-code ${fn}"
    # 注意：update-code 不會動環境變數，先前設好的機密會保留
    aws_cmd lambda update-function-code \
      --function-name "${fn}" \
      --zip-file "fileb://${zip}" \
      --query 'LastUpdateStatus' --output text
  fi

  rm -f bootstrap "${zip}"
}

main() {
  rm -f bootstrap
  deploy_target "analyze" "cmd/lambda-analyze" \
    "$(read_yaml '.deploy.analyze.function_name')" \
    "$(read_yaml '.deploy.analyze.timeout')" \
    "$(read_yaml '.deploy.analyze.memory_size')"
  deploy_target "slack" "cmd/lambda-slack" \
    "$(read_yaml '.deploy.slack.function_name')" \
    "$(read_yaml '.deploy.slack.timeout')" \
    "$(read_yaml '.deploy.slack.memory_size')"
  log "部署完成"
}

main "$@"
