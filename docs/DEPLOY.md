# 打包與部署（deploy.sh）

本文件說明如何用 [`deploy.sh`](../deploy.sh) 從本機打包，到首次部署／之後更新兩個 Lambda（`lambda-analyze`、`lambda-slack`）。

## 產出物

```bash
./deploy.sh [product]          # product 預設 goodnight
```

會在 `dist/` 產生：

| 檔案 | 用途 |
|------|------|
| `dist/lambda-analyze.zip` | 每日分析（EventBridge 觸發） |
| `dist/lambda-slack.zip` | Slack Interactive Webhook |

每個 zip 內含：

- `bootstrap` — Go 編譯的 Lambda 入口（`provided.al2023`）
- `config/<product>/config.yaml` — 打包當下的設定（無則用 example）
- `queries/<product>/*.sql` — 分析 SQL

預設編譯目標：`linux/arm64`（可用環境變數覆寫）。

## 前置條件

1. 本機 Go（預設尋找 `$HOME/sdk/go1.26.4/bin`）
2. AWS CLI 已登入，且有更新目標 Lambda 的權限（若要用腳本自動上傳）
3. Lambda runtime：`provided.al2023`，handler：`bootstrap`，架構與 zip 一致（預設 arm64）
4. Redshift 已套用 `migrations/001_agent_memory.sql`（guidelines／feedback 與分析同叢集）
5. 建議 secrets 用 **Lambda 環境變數**，不要把密碼打進 zip 內的 yaml

### 建議環境變數（兩個 Lambda 都可設）

| 變數 | 說明 |
|------|------|
| `DA_AGENT_PRODUCT` | 對應 `config/<product>`，預設 `default` |
| `REDSHIFT_CONN_STR` | Redshift（分析 SQL + memory 表） |
| `GEMINI_API_KEY` | Gemini |
| `SLACK_BOT_TOKEN` | `xoxb-...` |
| `SLACK_SIGNING_SECRET` | 驗證 Interactive Request（slack Lambda 必填） |
| `SLACK_CHANNEL_ID` | 報告 Channel（analyze 必填） |
| `DA_AGENT_WORKERS` | worker 數（可選） |

## 首次部署

### 1. 打包

```bash
cp config/goodnight/config.example.yaml config/goodnight/config.yaml
# 可只放非敏感預設；連線字串建議之後用 Lambda env

chmod +x deploy.sh
./deploy.sh goodnight
```

### 2. 建立 Lambda（若尚未建立）

在 AWS Console 或 CLI 建立兩個函數，例如：

- `da-agents-analyze`
- `da-agents-slack`

設定：

- Runtime：`provided.al2023`
- Architecture：`arm64`（若你改了 `GOARCH_TARGET=amd64` 則用 x86_64）
- Handler：`bootstrap`
- Timeout：analyze 建議 ≥ 5 分鐘；slack ≥ 30 秒
- Memory：analyze 建議 ≥ 512 MB
- VPC：若 Redshift 在私有網段，Lambda 需進相同 VPC 並開對應 SG

上傳對應 zip，並設定上表環境變數。

### 3. 綁定觸發

| 函數 | 觸發 |
|------|------|
| analyze | EventBridge Schedule（例如每天 09:00） |
| slack | Function URL 或 API Gateway HTTP API，路徑需能打到 `/slack/interactions` |

Slack App → **Interactivity Request URL** 指向：

```text
https://<function-url-or-apigw>/slack/interactions
```

### 4. 一鍵上傳（可選）

若函數已存在，可在打包後自動 `UpdateFunctionCode`：

```bash
export AWS_LAMBDA_ANALYZE_NAME=da-agents-analyze
export AWS_LAMBDA_SLACK_NAME=da-agents-slack
# 可選：AWS_PROFILE / AWS_REGION
./deploy.sh goodnight
```

未設定上述兩個名稱時，腳本只打包，不呼叫 AWS。

## 之後更新（程式／SQL／設定）

日常流程：

```bash
# 1. 改 code 或 queries/<product>/*.sql
# 2. 重新打包（+ 可選自動更新 Lambda）
export AWS_LAMBDA_ANALYZE_NAME=da-agents-analyze
export AWS_LAMBDA_SLACK_NAME=da-agents-slack
./deploy.sh goodnight
```

| 你改了什麼 | 要不要重部署 | 備註 |
|------------|--------------|------|
| Go 程式 | 要 | 重跑 `deploy.sh` |
| `queries/*.sql` | 要 | SQL 打進 zip，改完必須重打包 |
| `config.yaml` 打進 zip 的欄位 | 要 | 或改用 Lambda env，改 env 不必重打包 |
| Lambda 環境變數 / Timeout / VPC | 不必重打包 | Console 或 `aws lambda update-function-configuration` |
| Redshift guidelines／feedback | 不必 | 寫表即可，下次分析自動生效 |
| Slack App URL / Signing Secret | 不必重打包 | 改 Slack 設定或 Lambda env |

### 手動更新（不用腳本上傳時）

```bash
./deploy.sh goodnight

aws lambda update-function-code \
  --function-name da-agents-analyze \
  --zip-file fileb://dist/lambda-analyze.zip

aws lambda update-function-code \
  --function-name da-agents-slack \
  --zip-file fileb://dist/lambda-slack.zip
```

等 `LastUpdateStatus=Successful` 後再測。

## 腳本環境變數一覽

| 變數 | 預設 | 說明 |
|------|------|------|
| 第一個參數 `product` | `default` | `config/`、`queries/` 子目錄名 |
| `GOOS_TARGET` | `linux` | 編譯 OS |
| `GOARCH_TARGET` | `arm64` | 編譯架構（`amd64` 亦可） |
| `AWS_LAMBDA_ANALYZE_NAME` | （空） | 有值則上傳 analyze zip |
| `AWS_LAMBDA_SLACK_NAME` | （空） | 有值則上傳 slack zip |
| `AWS_PROFILE` / `AWS_REGION` | CLI 預設 | 傳給 aws 指令 |

## 驗證

1. **Analyze**：Console 對 `da-agents-analyze` 發 Test（空 JSON `{}`）或等排程；Slack Channel 應出現日報。
2. **Slack**：點「準確／糾正」；CloudWatch 無簽名錯誤；Redshift `agent_feedback` / `agent_guidelines` 有新列。
3. **本機對照**：同一套 config，先用 `go run ./cmd/analyze` 確認 SQL／Gemini／Slack 正常，再上雲。

## 常見問題

- **zip 裡還是 example config**：確認已建立 `config/<product>/config.yaml`，或完全依賴 Lambda env。
- **連不到 Redshift**：檢查 Lambda VPC、SG、連線字串、`sslmode`。
- **Slack 401 signature**：`SLACK_SIGNING_SECRET` 與 App 後台不一致，或 body 被 API Gateway 改寫。
- **架構不符**：`GOARCH_TARGET` 必須與 Lambda Architecture 一致。
