-- DA Agents 記憶庫 schema（Amazon Redshift）
-- 套用方式：psql "$REDSHIFT_CONN_STR" -f migrations/001_agent_memory.sql
--
-- 說明：
-- - 與分析用 metric 查詢同一套 Redshift。
-- - 小表使用 DISTSTYLE ALL（複製到各節點）。
-- - 不使用 Postgres 式 CREATE INDEX；改以 SORTKEY 輔助過濾。
-- - Redshift 不強制外鍵；derived_guideline_id 僅為邏輯關聯。

CREATE TABLE IF NOT EXISTS agent_guidelines (
    id            INT IDENTITY(1,1) PRIMARY KEY,
    category      VARCHAR(50) NOT NULL,          -- metric_logic | formatting | context | investigation
    rule_text     VARCHAR(65535) NOT NULL,       -- Prompt 指示（祈使句）
    is_active     BOOLEAN DEFAULT TRUE,          -- FALSE = 保留但不進 Prompt
    source        VARCHAR(50) DEFAULT 'manual',  -- manual | slack_feedback
    created_at    TIMESTAMP DEFAULT GETDATE(),
    updated_at    TIMESTAMP DEFAULT GETDATE()
)
DISTSTYLE ALL
SORTKEY (is_active, category, id);

CREATE TABLE IF NOT EXISTS agent_feedback (
    id                     INT IDENTITY(1,1) PRIMARY KEY,
    raw_text               VARCHAR(65535),       -- 糾正 Modal 原文；positive 可為空
    feedback_type          VARCHAR(20) NOT NULL, -- positive | correction
    slack_user_id          VARCHAR(50),
    slack_message_ts       VARCHAR(50),
    derived_guideline_id   INT,                  -- 邏輯關聯 agent_guidelines.id（Redshift 不強制 FK）
    created_at             TIMESTAMP DEFAULT GETDATE()
)
DISTSTYLE ALL
SORTKEY (feedback_type, created_at, id);

-- 僅在表為空時寫入預設準則
INSERT INTO agent_guidelines (category, rule_text, is_active, source)
SELECT v.category, v.rule_text, TRUE, 'manual'
FROM (
    SELECT 'formatting' AS category,
           '全程使用繁體中文撰寫摘要與洞察。使用簡潔條列，優先呈現最重要的異常，避免空話。' AS rule_text
    UNION ALL
    SELECT 'metric_logic',
           '不要把 DAU 與 MAU 混為一談。每個指標都必須標明所使用的日期區間。'
    UNION ALL
    SELECT 'context',
           '當資料同時包含昨日與上週對比時，必須一併比較並說明差異。'
    UNION ALL
    SELECT 'investigation',
           '標示異常時，只用繁體中文描述建議調查方向（例如切分維度、核對資料源）；不要提供任何 SQL 或程式碼範例，也不要臆造查核結果。'
) v
WHERE NOT EXISTS (SELECT 1 FROM agent_guidelines LIMIT 1);
