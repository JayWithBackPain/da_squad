-- @description:作為主要分析的起點

WITH RECURSIVE
    params AS (
        SELECT ({{run_date}} - 1)::date AS report_date,
            ({{run_date}} - 60)::date AS start_date
    ),

-- 1. 建立連續日期，確保 LAG(7) 永遠代表 7 天前
    calendar(dt) AS (
        SELECT start_date FROM params
        UNION ALL
        SELECT (c.dt + INTERVAL '1 day')::date FROM calendar c CROSS JOIN params p WHERE c.dt < p.report_date
    ),

-- 2. Activity Source：所有來自 daily_user_activities 的基礎指標
    source_activity AS (
        SELECT dua.dt,
               COUNT(DISTINCT dua.user_id) AS dau,
               count(distinct case when can_purchasing_dim is not null or sub_purchasing_dim is not null then dua.user_id end) as payers,
               count(distinct case when dua.matching_dim is not null then dua.user_id end) as matchers,
               count(distinct case when dua.audience_dim is not null then dua.user_id end) as audiences,
               SUM(dua.sub_purchasing_dim.revenue) AS sub_revenue,
               SUM(dua.can_purchasing_dim.revenue) AS can_revenue

        FROM datamart.daily_user_activities dua
                 CROSS JOIN params p
        WHERE dua.dt >= p.start_date AND dua.dt < p.report_date + 1
        GROUP BY dua.dt
    ),

-- 3. AdMob Source
    source_admob AS (
        SELECT ar.dt+'1 day'::interval as dt, SUM(ar.gross_revenue) AS admob_revenue
        FROM admob_revenue ar
                 CROSS JOIN params p
        WHERE ar.dt BETWEEN p.start_date AND p.report_date
        GROUP BY ar.dt
    ),

-- 4. Daily Wide Metrics：只負責整合不同資料來源
    daily_wide AS (
        SELECT c.dt,
               COALESCE(a.dau, 0)::decimal(18,2) AS dau,
            coalesce(a.payers,0)::decimal(18,2) as payers,
            coalesce(a.matchers, 0)::decimal(18,2) AS matchers,
            coalesce(a.audiences, 0)::decimal(18,2) AS audiences,
            COALESCE(a.sub_revenue, 0)::decimal(18,2) AS sub_revenue,
            COALESCE(a.can_revenue, 0)::decimal(18,2) AS can_revenue,
            COALESCE(ad.admob_revenue, 0)::decimal(18,2) AS admob_revenue,
            (COALESCE(a.sub_revenue, 0) + COALESCE(a.can_revenue, 0) + COALESCE(ad.admob_revenue, 0))::decimal(18,2) AS total_revenue,
            CASE WHEN a.dt IS NOT NULL THEN 1 ELSE 0 END AS activity_data_available,
               CASE WHEN ad.dt IS NOT NULL THEN 1 ELSE 0 END AS admob_data_available
        FROM calendar c
                 LEFT JOIN source_activity a ON c.dt = a.dt
                 LEFT JOIN source_admob ad ON c.dt = ad.dt
    ),

-- 5. Wide → Long：新增 Metric 時，主要修改這一層
    daily_metrics AS (
        SELECT dt, 'dau' AS metric, dau AS value, activity_data_available AS data_available FROM daily_wide
UNION ALL
SELECT dt, 'payers' AS metric, payers AS value, activity_data_available AS data_available FROM daily_wide
union all
SELECT dt, 'sub_revenue', sub_revenue, activity_data_available FROM daily_wide
UNION ALL
SELECT dt, 'can_revenue', can_revenue, activity_data_available FROM daily_wide
UNION ALL
SELECT dt, 'admob_revenue', admob_revenue, admob_data_available FROM daily_wide
UNION ALL
SELECT dt, 'total_revenue', total_revenue,
       CASE WHEN activity_data_available = 1 AND admob_data_available = 1 THEN 1 ELSE 0 END
FROM daily_wide
UNION ALL
select dt, 'matchers', matchers, activity_data_available from daily_wide
union all
select dt, 'audiences', audiences, activity_data_available from daily_wide
    ),

-- 6. 所有 Metric 共用同一套 Baseline
    metric_baselines AS (
        SELECT dt, metric, value, data_available,
               LAG(value, 1) OVER (PARTITION BY metric ORDER BY dt) AS previous_day,
               LAG(value, 7) OVER (PARTITION BY metric ORDER BY dt) AS last_week,
               ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING), 2) AS previous_7d_avg,
               ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 2) AS previous_30d_avg,
               ROUND(STDDEV_SAMP(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 2) AS previous_30d_stddev
        FROM daily_metrics
    ),

-- 7. 所有 Metric 共用同一套 Comparison
    metric_comparisons AS (
        SELECT *,
               ROUND((value / NULLIF(previous_day, 0) - 1) * 100, 2) AS dod_pct,
               ROUND((value / NULLIF(last_week, 0) - 1) * 100, 2) AS wow_pct,
               ROUND((value / NULLIF(previous_7d_avg, 0) - 1) * 100, 2) AS vs_7d_avg_pct,
               ROUND((value / NULLIF(previous_30d_avg, 0) - 1) * 100, 2) AS vs_30d_avg_pct,
               ROUND((value - previous_30d_avg) / NULLIF(previous_30d_stddev, 0), 2) AS z_score_30d
        FROM metric_baselines
    )


SELECT m.*
FROM metric_comparisons m
         CROSS JOIN params p
WHERE m.dt = p.report_date
ORDER BY m.metric;