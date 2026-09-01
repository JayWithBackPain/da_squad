-- @description:針對每天的traffic流量和revenue的 mrr 模型，new:當天新註冊用戶，churned:當天滿30天未活躍的用戶，sleeping:當天滿7天不活躍的用戶，resurrected:距離前一次活躍相隔超過30天
-- @role: supporting
-- @supports: main_kpi

WITH RECURSIVE
    params AS (
        SELECT
                    CURRENT_DATE-1 AS end_date,
            (CURRENT_DATE - 8)::date AS start_date,
            (CURRENT_DATE - 68)::date AS raw_start_date
    ),

-- =========================================================
-- 1. 最近 7 天報表日期
-- =========================================================
    calendar(dt) AS (
        SELECT start_date FROM params
        UNION ALL
        SELECT (c.dt + 1)::date
        FROM calendar c CROSS JOIN params p
        WHERE c.dt < p.end_date
    ),

-- =========================================================
-- 2. Traffic history
-- 需要足夠歷史資料判斷 resurrected / sleeping / churned
-- =========================================================
    base_traffic AS (
        SELECT
            da.day::date AS dt,
            da.userid,
            1 AS is_active,
            0::decimal(18,2) AS ext_revenue
        FROM dailyactives da
                 CROSS JOIN params p
        WHERE da.day >= p.raw_start_date
        GROUP BY 1,2
    ),

-- =========================================================
-- 3. Purchase history
-- Revenue MRR 需要前後兩段 30D，因此抓 67 天
-- =========================================================
    base_purchase AS (
        SELECT
            st.datecreated::date AS dt,
            st.userid,
            0 AS is_active,
            SUM(st.amountdollars)::decimal(18,2) AS ext_revenue
        FROM store_transaction st
                 CROSS JOIN params p
        WHERE st.datecreated >= p.raw_start_date
          AND st.datecreated < p.end_date + 1
          AND st.status IN ('AUTHORIZED','COMPLETED')
          AND st.amountdollars > 0
        GROUP BY 1,2
    ),

-- =========================================================
-- 4. Traffic + Purchase 合併成唯一 user-day
-- 取代 FULL OUTER JOIN
-- =========================================================
    base_user_day AS (
        SELECT
            dt,
            userid,
            MAX(is_active) AS is_active,
            SUM(ext_revenue) AS ext_revenue
        FROM (
                 SELECT dt, userid, is_active, ext_revenue FROM base_traffic
                 UNION ALL
                 SELECT dt, userid, is_active, ext_revenue FROM base_purchase
             ) x
        GROUP BY 1,2
    ),

-- =========================================================
-- 5. User dimension
-- users_data 只 join 一次，同時預先計算 crown period
-- =========================================================
    base_dimension AS (
        SELECT
            b.dt,
            b.userid,
            b.is_active,
            b.ext_revenue,
            CASE WHEN ud.country = 'US' THEN 'na'
                 WHEN ud.locale IN ('en','es','me') THEN ud.locale
                 ELSE 'others' END AS region,
            DATEDIFF(day, ud.datecreated::date, b.dt) AS day_index,
            CASE WHEN EXTRACT(day FROM b.dt) <= 15
                     THEN DATE_TRUNC('month',b.dt)::date
    ELSE (DATE_TRUNC('month',b.dt) + INTERVAL '15 days')::date END AS tier_start_dt
        FROM base_user_day b
                 LEFT JOIN users_data ud ON b.userid = ud.userid
    ),

-- =========================================================
-- 6. Crown logs
-- 只保留實際可能使用到的日期範圍
-- =========================================================
    crown_tier AS (
        SELECT
            ctl.datecreated::date AS dt,
            ctl.userid,
            MAX(ctl.crown_level) AS crown_level
        FROM fact.crown_level_log ctl
                 CROSS JOIN params p
        WHERE ctl.datecreated >= DATE_TRUNC('month',p.raw_start_date)
          AND ctl.datecreated < p.end_date + 1
        GROUP BY 1,2
    ),

-- =========================================================
-- 7. 加上 Crown Tier
-- =========================================================
    base_activity AS (
        SELECT
            b.dt,
            b.userid,
            b.is_active,
            b.ext_revenue,
            b.region,
            b.day_index,
            CASE WHEN MAX(ct.crown_level) >= 13 THEN 'golden'
                 WHEN MAX(ct.crown_level) >= 11 THEN 'titanium'
                 WHEN MAX(ct.crown_level) >= 6 THEN 'platinum'
                 WHEN MAX(ct.crown_level) >= 1 THEN 'red'
                 ELSE 'no' END AS tier
        FROM base_dimension b
                 LEFT JOIN crown_tier ct
                           ON ct.userid = b.userid
                               AND ct.dt BETWEEN b.tier_start_dt AND b.dt
        GROUP BY 1,2,3,4,5,6
    ),

-- =========================================================
-- 模組 A：Traffic MRR
-- 只使用真正 active 的 user-day
-- =========================================================
    traffic_base AS (
        SELECT
            dt,
            userid,
            day_index,
            tier,
            region
        FROM base_activity
        WHERE is_active = 1
    ),

    traffic_events AS (
        SELECT
            userid,
            day_index,
            tier,
            region,
            dt,
            LAG(dt) OVER (PARTITION BY userid ORDER BY dt) AS prev_active_dt,
            LEAD(dt) OVER (PARTITION BY userid ORDER BY dt) AS next_active_dt
        FROM traffic_base
    ),

    traffic_projection AS (
        SELECT
            dt,
            tier,
            region,
            CASE WHEN day_index = 0 THEN 'new'
                 WHEN prev_active_dt IS NOT NULL AND dt - prev_active_dt <= 7 THEN 'retaining'
                 WHEN prev_active_dt IS NOT NULL AND dt - prev_active_dt > 30 AND day_index >= 30 THEN 'resurrected'
                 ELSE 'other_active' END AS traffic_state
        FROM traffic_events
                 CROSS JOIN params p
        WHERE dt BETWEEN p.start_date AND p.end_date

        UNION ALL

        SELECT
            (dt + INTERVAL '8 days')::date AS dt,
            tier,
            region,
            'sleeping' AS traffic_state
        FROM traffic_events
                 CROSS JOIN params p
        WHERE (next_active_dt IS NULL OR next_active_dt > dt + 8)
          AND (dt + INTERVAL '8 days')::date BETWEEN p.start_date AND p.end_date

        UNION ALL

        SELECT
            (dt + INTERVAL '30 days')::date AS dt,
            tier,
            region,
            'churned' AS traffic_state
        FROM traffic_events
                 CROSS JOIN params p
        WHERE (next_active_dt IS NULL OR next_active_dt > dt + 30)
          AND (dt + INTERVAL '30 days')::date BETWEEN p.start_date AND p.end_date
    ),

    traffic_agg AS (
        SELECT
            dt,
            tier,
            region,
            SUM(CASE WHEN traffic_state = 'new' THEN 1 ELSE 0 END) AS traffic_new,
            SUM(CASE WHEN traffic_state = 'retaining' THEN 1 ELSE 0 END) AS traffic_retaining,
            SUM(CASE WHEN traffic_state = 'resurrected' THEN 1 ELSE 0 END) AS traffic_resurrected,
            SUM(CASE WHEN traffic_state = 'sleeping' THEN 1 ELSE 0 END) AS traffic_sleeping,
            SUM(CASE WHEN traffic_state = 'churned' THEN 1 ELSE 0 END) AS traffic_churned
        FROM traffic_projection
        GROUP BY 1,2,3
    ),

-- =========================================================
-- 模組 B：Revenue MRR
-- 先縮小成真正有 revenue 的 user-day
-- =========================================================
    purchase_activity AS (
        SELECT
            dt,
            userid,
            tier,
            region,
            day_index,
            ext_revenue
        FROM base_activity
        WHERE ext_revenue > 0
    ),

    rolling_revenue AS (
        SELECT
            c.dt,
            b.userid,
            b.tier,
            b.region,
            SUM(CASE WHEN b.dt = c.dt AND b.day_index < 30 THEN b.ext_revenue ELSE 0 END) AS new_revenue_today,
            SUM(CASE WHEN b.dt BETWEEN c.dt - 30 AND c.dt - 1 THEN b.ext_revenue ELSE 0 END) AS rev_past_30d,
            SUM(CASE WHEN b.dt BETWEEN c.dt - 60 AND c.dt - 31 THEN b.ext_revenue ELSE 0 END) AS rev_prev_30d
        FROM calendar c
                 JOIN purchase_activity b ON b.dt BETWEEN c.dt - 60 AND c.dt
        GROUP BY 1,2,3,4
    ),

    revenue_agg AS (
        SELECT
            dt,
            tier,
            region,
            SUM(new_revenue_today) AS revenue_new,
            SUM(CASE WHEN rev_prev_30d > 0 AND rev_past_30d = 0 THEN rev_prev_30d / 30.0 ELSE 0 END) AS revenue_churned,
            SUM(CASE WHEN rev_past_30d >= rev_prev_30d AND rev_prev_30d > 0 THEN (rev_past_30d - rev_prev_30d) / 30.0 ELSE 0 END) AS revenue_expansion,
            SUM(CASE WHEN rev_past_30d < rev_prev_30d AND rev_past_30d > 0 THEN (rev_prev_30d - rev_past_30d) / 30.0 ELSE 0 END) AS revenue_contraction
        FROM rolling_revenue
        GROUP BY 1,2,3
    ),

-- =========================================================
-- 8. 所有實際出現過的 region × tier
-- =========================================================
    dimensions AS (
        SELECT DISTINCT region, tier
        FROM base_activity
    ),

-- =========================================================
-- 9. Date × Region × Tier spine
-- 保證缺資料時仍輸出 0
-- =========================================================
    daily_spine AS (
        SELECT
            c.dt,
            d.region,
            d.tier
        FROM calendar c
                 CROSS JOIN dimensions d
    )

-- =========================================================
-- 最終聚合
-- =========================================================
SELECT
    s.dt,
    s.tier,
    s.region,
    COALESCE(t.traffic_new,0) AS traffic_new,
    COALESCE(t.traffic_retaining,0) AS traffic_retaining,
    COALESCE(t.traffic_resurrected,0) AS traffic_resurrected,
    COALESCE(t.traffic_sleeping,0) AS traffic_sleeping,
    COALESCE(t.traffic_churned,0) AS traffic_churned,
    ROUND(COALESCE(r.revenue_new,0)::NUMERIC,2) AS revenue_new,
    ROUND(COALESCE(r.revenue_expansion,0)::NUMERIC,2) AS revenue_expansion,
    ROUND(COALESCE(r.revenue_contraction,0)::NUMERIC,2) AS revenue_contraction,
    ROUND(COALESCE(r.revenue_churned,0)::NUMERIC,2) AS revenue_churned
FROM daily_spine s
         LEFT JOIN traffic_agg t
                   ON s.dt = t.dt
                       AND s.region = t.region
                       AND s.tier = t.tier
         LEFT JOIN revenue_agg r
                   ON s.dt = r.dt
                       AND s.region = r.region
                       AND s.tier = r.tier
ORDER BY s.dt DESC,s.region,s.tier;