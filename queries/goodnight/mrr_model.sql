-- @description:針對每天的traffic流量和revenue的 mrr 模型，new:當天新註冊用戶，churned:當天滿30天未活躍的用戶，sleeping:當天滿7天不活躍的用戶，resurrected:距離前一次活躍相隔超過30天
-- @role: supporting
-- @supports: main_kpi
WITH RECURSIVE calendar(dt) AS (
    -- [優化 1] 只產出最近 7 天的時間軸
    SELECT (CURRENT_DATE - 7)::date
    UNION ALL
    SELECT (dt + INTERVAL '1 day')::date
    FROM calendar
    WHERE dt < CURRENT_DATE
),

-- [優化 2] 精準底表裁切：近 7 天日曆 - 60 天滾動 = 只需要 67 天前的資料
               base_activity AS (
                   SELECT
                       dt,
                       CASE WHEN country_code = 'TW' THEN 'TW' ELSE 'Others' END AS region,
                       user_id,
                       day_index,
                       COALESCE(can_purchasing_dim.revenue, 0) AS can_revenue,
                       COALESCE(sub_purchasing_dim.revenue, 0) AS sub_revenue
                   FROM datamart.daily_user_activities
                   WHERE dt >= (select min(dt)-'60 days'::interval from calendar)
               ),

-- =========================================================
-- 模組 A：Traffic (生命週期流量模型)
-- =========================================================
               traffic_events AS (
                   SELECT
                       user_id,
                       region,
                       dt,
                       day_index,
                       LAG(dt) OVER (PARTITION BY user_id ORDER BY dt) AS prev_active_dt,
                       LEAD(dt) OVER (PARTITION BY user_id ORDER BY dt) AS next_active_dt
                   FROM base_activity
               ),
               traffic_projection AS (
                   -- 狀態 1: 當日有真實上線的活躍分類
                   SELECT
                       dt,
                       region,
                       CASE
                           WHEN day_index = 0 THEN 'new'
                           WHEN dt - prev_active_dt <= 7 THEN 'retaining'
                           WHEN dt - prev_active_dt > 30 AND day_index >= 30 THEN 'resurrected'
                           ELSE 'other_active'
                           END AS traffic_state
                   FROM traffic_events
                   -- [優化 3] 提早過濾：只保留近 7 天的上線紀錄
                   WHERE dt >= CURRENT_DATE - 7

                   UNION ALL

                   -- 狀態 2: Sleeping (剛好第 8 天未活躍)
                   SELECT
                       (dt + INTERVAL '8 days')::date AS dt,
                       region,
                       'sleeping' AS traffic_state
                   FROM traffic_events
                   WHERE (next_active_dt IS NULL OR next_active_dt > dt + 8)
                     -- [優化 3] 提早過濾：只允許投射結果落在近 7 天內
                     AND (dt + INTERVAL '8 days')::date BETWEEN CURRENT_DATE - 7 AND CURRENT_DATE

UNION ALL

-- 狀態 3: Churned Traffic (剛好第 30 天未活躍)
SELECT
    (dt + INTERVAL '30 days')::date AS dt,
    region,
    'churned' AS traffic_state
FROM traffic_events
WHERE (next_active_dt IS NULL OR next_active_dt > dt + 30)
  -- [優化 3] 提早過濾：只允許投射結果落在近 7 天內
  AND (dt + INTERVAL '30 days')::date BETWEEN CURRENT_DATE - 7 AND CURRENT_DATE
               ),
               traffic_agg AS (
SELECT
    dt,
    region,
    SUM(CASE WHEN traffic_state = 'new' THEN 1 ELSE 0 END) AS traffic_new,
    SUM(CASE WHEN traffic_state = 'retaining' THEN 1 ELSE 0 END) AS traffic_retaining,
    SUM(CASE WHEN traffic_state = 'resurrected' THEN 1 ELSE 0 END) AS traffic_resurrected,
    SUM(CASE WHEN traffic_state = 'sleeping' THEN 1 ELSE 0 END) AS traffic_sleeping,
    SUM(CASE WHEN traffic_state = 'churned' THEN 1 ELSE 0 END) AS traffic_churned
FROM traffic_projection
-- 這裡不需再寫 WHERE，因為 CTE 裡已經乾淨了
GROUP BY 1, 2
    ),

-- =========================================================
-- 模組 B：Revenue MRR (滾動營收模型)
-- =========================================================
    rolling_revenue AS (
SELECT
    c.dt,
    b.user_id,
    b.region,

    SUM(CASE WHEN b.dt = c.dt AND b.day_index < 30 THEN b.can_revenue ELSE 0 END) AS new_can_revenue_today,
    SUM(CASE WHEN b.dt BETWEEN c.dt - 30 AND c.dt - 1 THEN b.can_revenue ELSE 0 END) AS can_rev_past_30d,
    SUM(CASE WHEN b.dt BETWEEN c.dt - 60 AND c.dt - 31 THEN b.can_revenue ELSE 0 END) AS can_rev_prev_30d,

    SUM(CASE WHEN b.dt = c.dt AND b.day_index < 30 THEN b.sub_revenue ELSE 0 END) AS new_sub_revenue_today,
    SUM(CASE WHEN b.dt BETWEEN c.dt - 30 AND c.dt - 1 THEN b.sub_revenue ELSE 0 END) AS sub_rev_past_30d,
    SUM(CASE WHEN b.dt BETWEEN c.dt - 60 AND c.dt - 31 THEN b.sub_revenue ELSE 0 END) AS sub_rev_prev_30d

FROM calendar c
    JOIN base_activity b
ON b.dt BETWEEN c.dt - 60 AND c.dt
    -- [修復] 之前筆誤寫了兩次 sub_revenue
    AND (b.sub_revenue > 0 OR b.can_revenue > 0)
GROUP BY 1, 2, 3
    ),
    revenue_agg AS (
SELECT
    dt,
    region,
    -- 1. Sub Revenue
    SUM(new_sub_revenue_today) AS sub_revenue_new,
    SUM(CASE WHEN sub_rev_prev_30d > 0 AND sub_rev_past_30d = 0 THEN sub_rev_prev_30d / 30.0 ELSE 0 END) AS sub_revenue_churned,
    SUM(CASE WHEN sub_rev_past_30d >= sub_rev_prev_30d AND sub_rev_prev_30d > 0 THEN (sub_rev_past_30d - sub_rev_prev_30d) / 30.0 ELSE 0 END) AS sub_revenue_expansion,
    SUM(CASE WHEN sub_rev_past_30d < sub_rev_prev_30d AND sub_rev_past_30d > 0 THEN (sub_rev_prev_30d - sub_rev_past_30d) / 30.0 ELSE 0 END) AS sub_revenue_contraction,

    -- 2. Can Revenue
    SUM(new_can_revenue_today) AS can_revenue_new,
    SUM(CASE WHEN can_rev_prev_30d > 0 AND can_rev_past_30d = 0 THEN can_rev_prev_30d / 30.0 ELSE 0 END) AS can_revenue_churned,
    SUM(CASE WHEN can_rev_past_30d >= can_rev_prev_30d AND can_rev_prev_30d > 0 THEN (can_rev_past_30d - can_rev_prev_30d) / 30.0 ELSE 0 END) AS can_revenue_expansion,
    SUM(CASE WHEN can_rev_past_30d < can_rev_prev_30d AND can_rev_past_30d > 0 THEN (can_rev_prev_30d - can_rev_past_30d) / 30.0 ELSE 0 END) AS can_revenue_contraction

FROM rolling_revenue
GROUP BY 1, 2
    )

-- =========================================================
-- 最終聚合
-- =========================================================
SELECT
    COALESCE(t.dt, r.dt) AS dt,
    COALESCE(t.region, r.region) AS region,

    COALESCE(t.traffic_new, 0) AS traffic_new,
    COALESCE(t.traffic_retaining, 0) AS traffic_retaining,
    COALESCE(t.traffic_resurrected, 0) AS traffic_resurrected,
    COALESCE(t.traffic_sleeping, 0) AS traffic_sleeping,
    COALESCE(t.traffic_churned, 0) AS traffic_churned,

    ROUND(COALESCE(r.sub_revenue_new, 0)::NUMERIC, 2) AS sub_revenue_new,
    ROUND(COALESCE(r.sub_revenue_expansion, 0)::NUMERIC, 2) AS sub_revenue_expansion,
    ROUND(COALESCE(r.sub_revenue_contraction, 0)::NUMERIC, 2) AS sub_revenue_contraction,
    ROUND(COALESCE(r.sub_revenue_churned, 0)::NUMERIC, 2) AS sub_revenue_churned,
    ROUND(COALESCE(r.can_revenue_new, 0)::NUMERIC, 2) AS can_revenue_new,
    ROUND(COALESCE(r.can_revenue_expansion, 0)::NUMERIC, 2) AS can_revenue_expansion,
    ROUND(COALESCE(r.can_revenue_contraction, 0)::NUMERIC, 2) AS can_revenue_contraction,
    ROUND(COALESCE(r.can_revenue_churned, 0)::NUMERIC, 2) AS can_revenue_churned

FROM traffic_agg t
         FULL OUTER JOIN revenue_agg r
                         ON t.dt = r.dt AND t.region = r.region
ORDER BY 1 DESC, 2;