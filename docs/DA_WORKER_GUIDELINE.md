# DA Worker：Query 結果格式與撰寫準則

DA Worker **只負責執行** `queries/<product>/*.sql` 並產出 `MetricResult`，**不呼叫 LLM**。  
Report Agent 會把所有 worker 結果＋ Redshift guidelines 一次送給 Gemini。

本文是寫 SQL／設計指標時的契約說明。

---

## 1. Worker 回傳結構（程式契約）

每個 `.sql` 檔對應一筆 `MetricResult`（見 `internal/worker`）：

```json
{
  "name": "example_dau",
  "columns": ["report_date", "dau", "paying_users", "conversion_rate"],
  "rows": [
    {
      "report_date": "2026-07-23",
      "dau": "1000",
      "paying_users": "120",
      "conversion_rate": "0.12"
    }
  ],
  "markdown": "### example_dau\n| report_date | dau | ... |\n| --- | --- | ... |\n| 2026-07-23 | 1000 | ... |\n\n_rows=1_\n",
  "error": ""
}
```

| 欄位 | 說明 |
|------|------|
| `name` | 檔名去掉 `.sql`（例如 `dau.sql` → `dau`） |
| `columns` | SELECT 欄位名，順序與 SQL 一致 |
| `rows` | 列資料；**所有值已轉成 string**（含數字、日期、`NULL`→`"NULL"`） |
| `markdown` | 自動產生的精簡表格，給 Report Agent 當 context |
| `error` | 查詢失敗時填錯誤訊息；成功為空。單檔失敗**不中斷**其他 worker |

列數上限：`analyze.max_rows_per_query`（預設 **200**）。超過會截斷，只保留前 N 列。

---

## 2. SQL 檔案規則

| 規則 | 說明 |
|------|------|
| 路徑 | `queries/<product>/*.sql`（目前非遞迴） |
| 命名 | `snake_case`，語意清楚：`dau.sql`、`paying_conversion.sql` |
| 一檔一主題 | 一個檔只服務一個指標／一塊摘要，方便報告引用 `name` |
| 只做聚合 | **禁止**把 raw event／明細大表丟給 Agent；先 `GROUP BY` / 視窗聚合 |
| 可單獨執行 | 在 Redshift 直接跑應能出結果，不依賴 Go 傳入參數（MVP 無 `$1` 綁定） |
| 日期窗寫清楚 | 在 SELECT 帶出 `report_date` 或 `date_from` / `date_to`，讓模型知道時間範圍 |
| 標註角色（建議） | 檔案開頭用 `-- @desc` / `-- @role` / `-- @supports` 告訴 Report Agent 如何交叉參照 |

### 指標元資料（給模型交叉參照）

在 `.sql` **最上方**加註解（會進 Prompt，不會當業務 SQL 邏輯）：

```sql
-- @desc: 付費用戶消費的人均、中位數、P90
-- @role: supporting
-- @supports: revenue_daily, arpu
SELECT ...
```

| 指令 | 說明 |
|------|------|
| `@desc` | 這支 query 在查什麼（一句話） |
| `@role` | `primary`（主角 KPI）／`supporting`（診斷依據）／`context`（背景）；省略時預設 `primary` |
| `@supports` | 若是 supporting，列出它服務的主要指標檔名（不含 `.sql`），逗號分隔。報告內文會轉成業務名稱，不會提檔名 |

Report Agent 會被要求：**交叉參照所有結果成一篇敘事**；supporting 用來解釋 primary。內文預設不必點名指標，僅必要時才說明參考了哪個 metrics。歸因禁止指向系統／ETL。

範例形狀（建議）：

```sql
SELECT
    CURRENT_DATE - 1 AS report_date,
    COUNT(DISTINCT user_id) AS dau,
    COUNT(DISTINCT CASE WHEN is_paying THEN user_id END) AS paying_users,
    ROUND(paying_users::float / NULLIF(dau, 0), 4) AS conversion_rate
FROM ...
WHERE event_date = CURRENT_DATE - 1;
```

（實際語法依你們 Redshift schema 調整；重點是**少量欄位 + 明確時間 + 聚合**。）

---

## 3. 結果形狀建議（給 Report Agent 好讀）

### 優先：單列 KPI 快照

適合 DAU、轉換率、營收等「昨天一個點」：

```text
columns: report_date, metric_a, metric_b, ...
rows: 剛好 1 列
```

### 次佳：短時間序列

適合 WoW / 近 7 日趨勢：

```text
columns: report_date, value, ...
rows: 7～14 列（遠小於 max_rows）
```

### 避免

- 寬表（幾十個幾乎無關的欄位塞同一檔）
- 上百列明細（即使用不完也會佔 token；且超過 200 會被截斷）
- 無欄位別名的 `SELECT *`
- 把多個無關業務混在同一 SQL（應拆檔）

### 欄位命名

- 用英文 `snake_case`：`dau`、`arpu`、`wow_delta`
- 比率用小數（`0.12`）或明確單位欄位名（`conversion_rate_pct` 若用 12 表示 12%）
- 對比欄位成對出現：`dau` + `dau_prev_day`，或 `value` + `wow_delta`

---

## 4. Markdown 長什麼樣（自動產生）

Worker 會把結果編成類似：

```markdown
### example_dau
| report_date | dau | paying_users | conversion_rate |
| --- | --- | --- | --- |
| 2026-07-23 | 1000 | 120 | 0.12 |

_rows=1_
```

失敗時 Report 會看到：

```markdown
### example_dau
ERROR: query: ...
```

你不需要手寫 markdown；把 SQL 結果設計好即可。

---

## 5. 撰寫準則（Rules checklist）

寫／改一個 DA worker SQL 前對一下：

1. **聚合優先**：輸出是摘要，不是 raw log。  
2. **時間窗可見**：結果列裡看得到報告日或起迄日。  
3. **可解釋**：欄位名讓不懂 SQL 的人也能猜用途。  
4. **夠小**：預設以「個位數～數十列」為目標，絕少逼近 200。  
5. **可對比**：盡量附前期／上週，方便一次 Gemini 找異常。  
6. **失敗隔離**：假設這個檔掛了，其他指標仍應能出報告。  
7. **不依賴 LLM**：worker 不做解讀；解讀留給 Report Agent。  
8. **產品隔離**：不同 product 的 SQL 放在各自 `queries/<product>/`。

---

## 6. 與 Report Agent / Memory 的關係

```text
queries/*.sql  →  DA Workers (N)  →  []MetricResult
                                      ↓
Redshift agent_guidelines (is_active)  →  Report Agent (1× Gemini)  →  Slack
```

- SQL 結果進 Prompt 的是 **markdown 摘要**，不是整份 raw JSON。  
- 業務解讀規則（例如「DAU 不要跟 MAU 混」）應寫進 Redshift `agent_guidelines`，或透過 Slack「糾正」進化，**不要**寫死在 SQL 註解裡指望模型去讀檔案註解。  
- SQL 註解可給人類維護者看；執行時只送 query 結果。

---

## 7. 本機快速驗證單一 SQL

在 Redshift 客戶端先跑通後，再放進 repo：

```bash
# 整批（需完整 config）
go run ./cmd/analyze -product goodnight
```

新增檔案後無需改 Go 碼；下次 analyze／重新 `deploy.sh` 打包即可被 worker 消化。
