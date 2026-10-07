# 打包與部署

`deploy.sh <product>` 從 `config/<product>/config.yaml` 的 deploy 區塊讀取設定，建立／更新 analyze 與 slack 兩個 Lambda。product 是必填參數；腳本會上傳部署，不是只產生本機 zip。

## V1 learning loop 的部署順序

先套用 [002 migration](../migrations/002_learning_loop.sql) 、[003 migration](../migrations/003_feedback_reviews.sql) 與 [004 migration](../migrations/004_replay.sql)，再更新程式。既有 `da_squad.agent_guidelines` 不變；執行角色需能寫入新 run/payload/feedback 表，feedback ingestion 使用 transaction table lock。

新安裝需要 001–004；已有原 guideline 表的安裝套用 002–004。003 新增 PO review 稽核表，004 新增回放 session、attempt 與 knowledge release。詳細設定、回滾與 feedback retry 見 [WORKFLOW.md](WORKFLOW.md)。

## 本機設定

```bash
mkdir -p config/goodnight
cp config/default/config.example.yaml config/goodnight/config.yaml
psql "$REDSHIFT_CONN_STR" -v ON_ERROR_STOP=1 -f migrations/002_learning_loop.sql
psql "$REDSHIFT_CONN_STR" -v ON_ERROR_STOP=1 -f migrations/003_feedback_reviews.sql
psql "$REDSHIFT_CONN_STR" -v ON_ERROR_STOP=1 -f migrations/004_replay.sql
```

新安裝另需先套用 001；da_squad schema 由既有環境管理。同名表若已存在但欄位不同，CREATE TABLE IF NOT EXISTS 不會升級，先核對定義。

```yaml
analyze:
  timezone: Asia/Taipei
  knowledge_path: ""  # 自動選 knowledge/<product>/catalog.yaml；"-" 明確停用
  max_input_tokens: 12000
  max_input_bytes: 60000
  max_knowledge_bytes: 8000
  max_knowledge_items: 12
```

query_dir 空白時按產品選 queries/<product>。無效日期、timezone 與負數／矛盾預算在連線前拒絕。knowledge YAML 依產品打包，預設會載入；draft 不影響分析。知識停用時 legacy DB guidelines 仍經預算選取。

## 前置條件

- Go 1.26.4；腳本在找不到 go 時會嘗試 `$HOME/sdk/go1.26.4/bin`。
- yq、zip 與已登入的 AWS CLI。
- `config/<product>/config.yaml` 及 `queries/<product>`。
- deploy.lambda_role 與兩個 function_name。runtime 使用 provided.al2023，handler 為 bootstrap。

部署設定全部讀 YAML，architecture 預設 arm64，可填 x86_64。region／aws_profile 留空時使用 AWS CLI 預設。

```yaml
deploy:
  region: ""
  aws_profile: ""
  lambda_role: "arn:aws:iam::<account>:role/<role>"
  architecture: arm64
  analyze:
    function_name: da-agents-analyze
    timeout: 900
    memory_size: 1024
  slack:
    function_name: da-agents-slack
    timeout: 900
    memory_size: 512
```

## 打包內容與執行

```bash
./deploy.sh goodnight
```

每個包包含 bootstrap、config/<product>、queries/<product>，以及存在時的 knowledge/<product>。使用暫存 deployed_<product>_<target>.zip，完成後移除 bootstrap／zip；不保留 dist/ 產物。

不要把機密寫進打包的 YAML；以 Lambda env 提供 REDSHIFT_CONN_STR、GEMINI_API_KEY、SLACK_BOT_TOKEN、SLACK_SIGNING_SECRET、SLACK_CHANNEL_ID。

新建函數只帶 DA_AGENT_PRODUCT／DEPLOY_TIME，需再補機密設定。既有函數更新 code 與 timeout，保留 env、memory 與 role。slack Lambda 背景執行完整回放分析，deploy.sh 會將其 timeout 至少設為 900 秒；HTTP handler 的 3 秒內 ack 預算保持不變。memory 或 role 變更需另行更新 configuration。

analyze 綁定 EventBridge。slack 使用 Function URL／API Gateway，路徑 `/slack/interactions`，Slack App 設定 Interactivity URL。slack execution role 需要 lambda:InvokeFunction 指向自身，供背景 candidate 提煉與逐日 replay。該角色也需有原本 analyze 所需的 Redshift 網路／讀寫與 Gemini 連線能力。

## 驗證

確認一份新報告有 run 紀錄與 input/context/report/validation payload。按糾正後先確認原文已進 da_squad.feedback，再確認 status 變 candidate；候選不應自動新增 active guideline。

量測 Redshift ingestion 與 Lambda self-invoke 延遲，確認 Slack modal 不逾時。pending／failed 可使用 `cmd/feedback` 重試。核對 token overflow 會保留 failed run 而非正常報告。

一般每日分析的 knowledge YAML 修改需重新部署；歷史回放可透過 cmd/knowledge -publish 更新 Redshift catalog，不需重新部署。既有 DB guidelines 修改會在下一個 run 重讀。回滾程式／YAML 可使用前版部署包，保留新增資料表與歷史紀錄。

## 延遲與回復限制

Redshift write／table lock 可能超過 Slack deadline。Modal 保存預算 1.8 秒、enqueue 0.5 秒，整個 handler 約 2.5 秒 context 上限；仍須量測網路與提交延遲。DB 未確認保存就返回 modal error，保留 PO 輸入。

原文已保存但 enqueue 失敗時清除 modal，留在 pending，使用 feedback-list／retry-pending 恢復。尚無耐久 outbox／自動掃描器。本機背景工作有 3 分鐘 timeout；進程中止也需 recovery。

Slack 已送出而 mapping 寫入失敗時，依 log 的 run/channel/ts 核對；delivery_unknown 不盲目重發。回滾 YAML／程式版本時保留 run、feedback 與 review 稽核，不刪除 DB 表。


## Slack thread feedback 與回放啟用

Interactivity URL：`https://<endpoint>/slack/interactions`，供準確、糾正與下一天按鈕。

Events API Request URL：`https://<endpoint>/slack/events`。啟用 Event Subscriptions；公開 channel 訂閱 `message.channels` 並授予 bot `channels:history`，私人 channel 訂閱 `message.groups` 並授予 `groups:history`。權限變更後重新安裝 Slack App，邀請 bot 進入回放 channel。保留原本 chat:write 與 Interactivity 設定。API Gateway 若有固定路由，也須加入 /slack/events；Function URL 直接由 Go mux 分流。

驗證 challenge 後，在新報告 thread 回覆一則文字，確認 feedback 保存與 candidate 生成。Events ingestion 若 DB/queue 失敗會回 503，讓 Slack retry；event_id 保持一致，不新增重複 feedback。仍可能有 pending，使用 recovery CLI。

開始前可先 `TO=2026-01-03` 做三天試跑，再建立完整 session；完整 session 的日期與 channel 在建立時固定。Lambda 在同一函數 self-invoke 背景分析，沒有新增 Lambda／SQS；需實測 warehouse、Slack 寫入 deadline 及 Lambda timeout。不要把 test pass 當成已部署或已驗證線上 Redshift。
