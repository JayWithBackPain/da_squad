-- @description:每天的直播資訊，直撥主人數和內容時長，罐頭的營收與直撥行為有直接關係
-- @role: supporting
-- @supports: main_kpi

WITH RECURSIVE
    params AS (
        SELECT (CURRENT_DATE - 1)::date AS report_date,
            (CURRENT_DATE - 60)::date AS start_date
    ),

-- 1. 建立連續日期，確保 LAG(7) 永遠代表 7 天前
    calendar(dt) AS (
        SELECT start_date FROM params
        UNION ALL
        SELECT (c.dt + INTERVAL '1 day')::date FROM calendar c CROSS JOIN params p WHERE c.dt <= p.report_date
    ),

    -- 2. Activity Source：所有來自 daily_user_activities 的基礎指標
    activity_srouce AS (
        SELECT date_trunc('day',start_time) as dt,
               COUNT(DISTINCT case when is_video then s.user_id end) AS video_streamers,
               COUNT(DISTINCT case when not is_video then s.user_id end) AS voice_streamers,
               round(sum(case when is_video then duration else 0 end)::decimal(18,2)/3600,2) AS video_duration,
               round(sum(case when not is_video then duration else 0 end)::decimal(18,2)/3600,2) AS voice_duration
        FROM fact.streaming s
                 CROSS JOIN params p
        WHERE s.start_time BETWEEN p.start_date AND p.report_date + 1
        GROUP BY dt
    ),

    daily_wide as (
        SELECT calendar.dt,
               video_streamers,
               voice_streamers,
               video_duration,
               voice_duration
        FROM calendar left join activity_srouce on calendar.dt = activity_srouce.dt
    ),

    -- 3. Wide → Long：新增 Metric 時，主要修改這一層
    daily_metrics AS (
        SELECT dt, 'video_das' AS metric, video_streamers AS value FROM daily_wide
UNION ALL
SELECT dt, 'voice_das' AS metric, voice_streamers AS value FROM daily_wide
UNION ALL
SELECT dt, 'video_duration' AS metric, video_duration AS value FROM daily_wide
UNION ALL
SELECT dt, 'voice_duration' AS metric, voice_duration AS value FROM daily_wide
    ),

-- 4. 所有 Metric 共用同一套 Baseline
    metric_baselines AS (
SELECT dt, metric, value,
    LAG(value, 1) OVER (PARTITION BY metric ORDER BY dt) AS previous_day,
    LAG(value, 7) OVER (PARTITION BY metric ORDER BY dt) AS last_week,
    ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 7 PRECEDING AND 1 PRECEDING), 2) AS previous_7d_avg,
    ROUND(AVG(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 2) AS previous_30d_avg,
    ROUND(STDDEV_SAMP(value) OVER (PARTITION BY metric ORDER BY dt ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING), 2) AS previous_30d_stddev
FROM daily_metrics
    ),

-- 5. 所有 Metric 共用同一套 Comparison
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