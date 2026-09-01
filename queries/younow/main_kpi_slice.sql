-- @description:把 main_kpi 切成tier/region的切片
-- @role: supporting
-- @supports: main_kpi

WITH RECURSIVE
    params AS (
        SELECT
            (CURRENT_DATE - 2)::date AS report_date,
            (CURRENT_DATE - 61)::date AS start_date,
            (CURRENT_DATE - 77)::date AS crown_start_date
    ),

-- =========================================================
-- 1. Calendar
-- =========================================================
    calendar(dt) AS (
        SELECT start_date FROM params

        UNION ALL

        SELECT (c.dt + 1)::date
        FROM calendar c
                 CROSS JOIN params p
        WHERE c.dt < p.report_date
    ),

-- =========================================================
-- 2. Daily Active Base
-- =========================================================
    activity_base AS (
        SELECT
            da.day::date AS dt,
            da.userid,
            da.revenue,
            da.bars_spent,
            CASE WHEN ud.country = 'US' THEN 'na'
                 WHEN ud.locale IN ('en','es','me') THEN ud.locale
                 ELSE 'others' END AS region,
            CASE WHEN EXTRACT(day FROM da.day) <= 15
                     THEN DATE_TRUNC('month',da.day)::date
    ELSE (DATE_TRUNC('month',da.day) + INTERVAL '15 days')::date END AS tier_start_dt
    FROM dailyactives da
    CROSS JOIN params p
    LEFT JOIN users_data ud ON da.userid = ud.userid
    WHERE da.day BETWEEN p.start_date AND p.report_date
),

-- =========================================================
-- 3. Crown Tier
-- =========================================================
crown_tier AS (
    SELECT
        ctl.datecreated::date AS dt,
        ctl.userid,
        MAX(ctl.crown_level) AS crown_level
    FROM fact.crown_level_log ctl
    CROSS JOIN params p
    WHERE ctl.datecreated >= p.crown_start_date
      AND ctl.datecreated < p.report_date + 1
    GROUP BY 1,2
),

-- =========================================================
-- 4. User Daily Tier
-- =========================================================
base AS (
    SELECT
        a.dt,
        a.userid,
        a.region,
        a.revenue,
        a.bars_spent,
        MAX(ct.crown_level) AS level
    FROM activity_base a
    LEFT JOIN crown_tier ct
        ON ct.userid = a.userid
       AND ct.dt BETWEEN a.tier_start_dt AND a.dt
    GROUP BY 1,2,3,4,5
),

-- =========================================================
-- 5. Daily Aggregation
-- =========================================================
source_activity AS (
    SELECT
        dt,
        region,
        CASE WHEN level >= 13 THEN 'golden'
             WHEN level >= 11 THEN 'titanium'
             WHEN level >= 6 THEN 'platinum'
             WHEN level >= 1 THEN 'red'
             ELSE 'no' END AS tier,
        COUNT(DISTINCT userid) AS dau,
        COUNT(DISTINCT CASE WHEN revenue > 0 THEN userid END) AS payers,
        COUNT(DISTINCT CASE WHEN bars_spent > 0 THEN userid END) AS spenders,
        SUM(revenue) AS rev
    FROM base
    GROUP BY 1,2,3
),

-- =========================================================
-- 6. 實際存在的 Region × Tier
-- =========================================================
dimensions AS (
    SELECT DISTINCT region,tier
    FROM source_activity
),

-- =========================================================
-- 7. Calendar × Dimension Spine
-- =========================================================
daily_wide AS (
    SELECT
        c.dt,
        d.region,
        d.tier,
        COALESCE(a.dau,0)::integer AS dau,
        COALESCE(a.payers,0)::integer AS payers,
        COALESCE(a.spenders,0)::integer AS spenders,
        COALESCE(a.rev,0)::decimal(38,2) AS rev,
        CASE WHEN a.dt IS NOT NULL THEN 1 ELSE 0 END AS activity_data_available
    FROM calendar c
    CROSS JOIN dimensions d
    LEFT JOIN source_activity a
        ON c.dt = a.dt
       AND d.region = a.region
       AND d.tier = a.tier
),

-- =========================================================
-- 8. Wide → Long
-- =========================================================
daily_metrics AS (
    SELECT dt,tier,region,'dau' AS metric,dau::decimal(38,4) AS value,activity_data_available AS data_available FROM daily_wide
    UNION ALL
    SELECT dt,tier,region,'payers',payers::decimal(38,4),activity_data_available FROM daily_wide
    UNION ALL
    SELECT dt,tier,region,'spenders',spenders::decimal(38,4),activity_data_available FROM daily_wide
    UNION ALL
    SELECT dt,tier,region,'rev',rev::decimal(38,4),activity_data_available FROM daily_wide
),

-- =========================================================
-- 9. Baseline
-- =========================================================
metric_baselines AS (
    SELECT
        dt,
        tier,
        region,
        metric,
        value,
        data_available,
        LAG(value,1) OVER (PARTITION BY region,tier,metric ORDER BY dt) AS previous_day,
        LAG(value,7) OVER (PARTITION BY region,tier,metric ORDER BY dt) AS last_week,
        ROUND(AVG(value) OVER (PARTITION BY region,tier,metric ORDER BY dt ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING),2) AS previous_7d_avg,
        ROUND(AVG(value) OVER (PARTITION BY region,tier,metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING),2) AS previous_30d_avg,
        ROUND(STDDEV_SAMP(value) OVER (PARTITION BY region,tier,metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING),2) AS previous_30d_stddev
    FROM daily_metrics
),

-- =========================================================
-- 10. Comparison
-- =========================================================
metric_comparisons AS (
    SELECT
        *,
        ROUND((value / NULLIF(previous_day,0) - 1) * 100,2) AS dod_pct,
        ROUND((value / NULLIF(last_week,0) - 1) * 100,2) AS wow_pct,
        ROUND((value / NULLIF(previous_7d_avg,0) - 1) * 100,2) AS vs_7d_avg_pct,
        ROUND((value / NULLIF(previous_30d_avg,0) - 1) * 100,2) AS vs_30d_avg_pct,
        ROUND((value - previous_30d_avg) / NULLIF(previous_30d_stddev,0),2) AS z_score_30d
    FROM metric_baselines
)

SELECT m.*
FROM metric_comparisons m
         CROSS JOIN params p
WHERE m.dt = p.report_date
ORDER BY m.region,m.tier,m.metric;