-- @description:作為主要分析的起點

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
    base AS (
        SELECT
            da.day::date AS dt,
            da.userid,
            sum(bars_spent) as bars_spent
        FROM dailyactives da
                 CROSS JOIN params p
        WHERE da.day BETWEEN p.start_date AND p.report_date
        group by 1,2
    ),
    ext_revenue as (
        select
            date_trunc('day',datecreated) as dt,
            count(distinct userid) as payers,
            sum(amountdollars) as rev
        from store_transaction
        where datecreated >= (select start_date from params)
          and status in ('AUTHORIZED','COMPLETED')
          and amountdollars > 0
        group by 1
    ),

-- =========================================================
-- 5. Daily Aggregation
-- =========================================================
    source_activity AS (
        SELECT
            dt,
            COUNT(DISTINCT userid) AS dau,
            COUNT(DISTINCT CASE WHEN bars_spent > 0 THEN userid END) AS spenders
        FROM base
        GROUP BY 1
    ),

-- =========================================================
-- 6. 實際存在的 Region × Tier // 棄用
-- =========================================================

-- =========================================================
-- 7. Calendar × Dimension Spine
-- =========================================================
    daily_wide AS (
        SELECT
            c.dt,
            COALESCE(a.dau,0)::integer AS dau,
            COALESCE(er.payers,0)::integer AS payers,
            COALESCE(a.spenders,0)::integer AS spenders,
            COALESCE(er.rev,0)::decimal(38,2) AS rev,
            CASE WHEN a.dt IS NOT NULL THEN 1 ELSE 0 END AS activity_data_available
        FROM calendar c
                 LEFT JOIN source_activity a ON c.dt = a.dt
                 left join ext_revenue er on c.dt = er.dt
    ),

-- =========================================================
-- 8. Wide → Long
-- =========================================================
    daily_metrics AS (
        SELECT dt,'dau' AS metric,dau::decimal(38,4) AS value,activity_data_available AS data_available FROM daily_wide
UNION ALL
SELECT dt,'payers',payers::decimal(38,4),activity_data_available FROM daily_wide
UNION ALL
SELECT dt,'spenders',spenders::decimal(38,4),activity_data_available FROM daily_wide
UNION ALL
SELECT dt,'rev',rev::decimal(38,4),activity_data_available FROM daily_wide
    ),

-- =========================================================
-- 9. Baseline
-- =========================================================
    metric_baselines AS (
        SELECT
            dt,
            metric,
            value,
            data_available,
            LAG(value,1) OVER (PARTITION BY metric ORDER BY dt) AS previous_day,
            LAG(value,7) OVER (PARTITION BY metric ORDER BY dt) AS last_week,
            ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING),2) AS previous_7d_avg,
            ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING),2) AS previous_30d_avg,
            ROUND(STDDEV_SAMP(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING),2) AS previous_30d_stddev
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
ORDER BY m.metric;