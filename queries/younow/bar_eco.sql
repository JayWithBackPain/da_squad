WITH RECURSIVE
    params AS (
        SELECT
            (CURRENT_DATE - 1)::date AS report_date,
            (CURRENT_DATE - 61)::date AS start_date
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
    daily_agg as (
        select
            date_trunc('day', st.datecreated)::date as dt,
            sum(st.amountdollars) as ext_revenue,
            sum(st.amountbars) as total_bar_generated
        from store_transaction st
                 left join store_product sp
                           on st.productid = sp.id
                 cross join params p
        where st.datecreated >= p.start_date - 30
          and st.datecreated < p.report_date + 1
          and sp.name not in ('Games Free Bars Reward', 'Games Paid Bars Reward')
          and st.status in ('AUTHORIZED', 'COMPLETED')
        group by 1
    ),
    daily_metric as (
        select
            dt,
            sum(total_bar_generated) over (order by dt rows between 2 preceding and current row)
                 / nullif(sum(ext_revenue) over (order by dt rows between 2 preceding and current row), 0) as rolling_3d_bar_per_dollar,
            sum(total_bar_generated) over (order by dt rows between 15 preceding and current row)
                 / nullif(sum(ext_revenue) over (order by dt rows between 15 preceding and current row), 0) as rolling_16d_bar_per_dollar,
            sum(total_bar_generated) over (order by dt rows between 29 preceding and current row)
                 / nullif(sum(ext_revenue) over (order by dt rows between 29 preceding and current row), 0) as rolling_30d_bar_per_dollar
        from daily_agg
    ),

    daily_wide AS (
        SELECT
            c.dt,
            dm.rolling_3d_bar_per_dollar,
            dm.rolling_16d_bar_per_dollar,
            dm.rolling_30d_bar_per_dollar
        FROM calendar c
                 LEFT JOIN daily_metric dm ON c.dt = dm.dt
    ),
    daily_metrics AS (
        SELECT dt,'rolling_3d_bar_per_dollar' AS metric,rolling_3d_bar_per_dollar::decimal(38,4) AS value FROM daily_wide
UNION ALL
SELECT dt,'rolling_16d_bar_per_dollar',rolling_16d_bar_per_dollar::decimal(38,4) as value FROM daily_wide
UNION ALL
SELECT dt,'rolling_30d_bar_per_dollar',rolling_30d_bar_per_dollar::decimal(38,4) as value FROM daily_wide
    ),

-- =========================================================
-- 9. Baseline
-- =========================================================
    metric_baselines AS (
SELECT
    dt,
    metric,
    value,
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