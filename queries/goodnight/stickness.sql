-- @description:計算黏著指標 dau/rolling_wau 以及 dau/rolling_mau，以及附上分母作為變因判斷
-- @role:supporting
-- @supports:main_kpi

WITH RECURSIVE
    params AS (
        SELECT (CURRENT_DATE - 1)::date AS report_date
    ),

-- 計算 report date + 前 30 天 metric
    calendar(dt) AS (
        SELECT (report_date - 30)::date FROM params
        UNION ALL
        SELECT (c.dt + 1)::date FROM calendar c CROSS JOIN params p WHERE c.dt < p.report_date
    ),

-- 為了計算 calendar 最早一天的 rolling 30D MAU，需要再往前取 29 天
    raw_dau AS (
        SELECT dt,
               CASE WHEN country_code = 'TW' THEN 'TW' ELSE 'others' END AS region,
               user_id
        FROM datamart.daily_user_activities
        WHERE dt BETWEEN (SELECT report_date - 59 FROM params) AND (SELECT report_date FROM params)
    ),

-- 每日 DAU / rolling WAU / rolling MAU
    daily_wide AS (
        SELECT c.dt, r.region,
               COUNT(DISTINCT CASE WHEN r.dt = c.dt THEN r.user_id END) AS dau,
               COUNT(DISTINCT CASE WHEN r.dt BETWEEN c.dt - 6 AND c.dt THEN r.user_id END) AS rolling_7d_wau,
               COUNT(DISTINCT r.user_id) AS rolling_30d_mau
        FROM calendar c
                 LEFT JOIN raw_dau r ON r.dt BETWEEN c.dt - 29 AND c.dt
        GROUP BY 1, 2
    ),

-- Wide → Long
    daily_metrics AS (
        SELECT dt, region, 'dau/rolling_wau' AS metric,
               round(dau::decimal(18,4) / NULLIF(rolling_7d_wau, 0),2) AS value
FROM daily_wide
UNION ALL
SELECT dt, region, 'dau/rolling_mau',
       round(dau::decimal(18,4) / NULLIF(rolling_30d_mau, 0),2)
FROM daily_wide
UNION ALL
SELECT dt, region, 'rolling_7d_wau', rolling_7d_wau::decimal(18,4)
FROM daily_wide
UNION ALL
SELECT dt, region, 'rolling_30d_mau', rolling_30d_mau::decimal(18,4)
FROM daily_wide
    ),

-- Baseline：region + metric 分開計算
    metric_baselines AS (
        SELECT dt, region, metric, value,
               LAG(value, 1) OVER (PARTITION BY region, metric ORDER BY dt) AS previous_day,
               LAG(value, 7) OVER (PARTITION BY region, metric ORDER BY dt) AS last_week,
               ROUND(AVG(value) OVER (PARTITION BY region, metric ORDER BY dt ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING), 4) AS previous_7d_avg,
               ROUND(AVG(value) OVER (PARTITION BY region, metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 4) AS previous_30d_avg,
               ROUND(STDDEV_SAMP(value) OVER (PARTITION BY region, metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 4) AS previous_30d_stddev
        FROM daily_metrics
    ),

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
ORDER BY m.region, m.metric;