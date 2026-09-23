-- @description:把 revenue 跟 dau 掛上 crown level 的標籤，用在分析活躍和購買的影響來自哪個等級
-- @role: supporting
-- @supports: main_kpi
WITH
    /* ============================================================
       0. Parameters
       ============================================================ */
    params AS (
        SELECT
            (CURRENT_DATE - 1)::date AS report_date,
            (CURRENT_DATE - 61)::date AS start_date,
            (CURRENT_DATE - 77)::date AS crown_start_date
    ),

    /* ============================================================
       1. Daily Active
       ============================================================ */
    da AS (
        SELECT
    day::date AS dt,
    userid,
    case when revenue > 0 then true else false end as is_payer
        FROM dailyactives
        CROSS JOIN params p
        WHERE day >= p.start_date
            AND day < p.report_date + 1
    ),

    /* ============================================================
       2. Crown level daily snapshot
       ============================================================ */
    cl AS (
        SELECT
            date_trunc('day', datecreated)::date AS dt,
            userid,
            max(crown_level) AS crown_level
        FROM fact.crown_level_log
        CROSS JOIN params p
        WHERE datecreated >= p.crown_start_date
            AND datecreated < p.report_date + 1
        GROUP BY 1,2
    ),

    /* ============================================================
       3. Active + Crown
       ============================================================ */
    da_with_crown AS (
        SELECT
            da.dt,
            da.userid,
            da.is_payer,
            coalesce(cl.crown_level,0) as crown_level,
            row_number() over (
                partition by da.userid,da.dt
                order by cl.dt desc
            ) as rn
        FROM da
        LEFT JOIN cl
            ON cl.userid = da.userid
            AND cl.dt <= da.dt
            AND cl.dt >= da.dt - interval '30 days'
    ),

    /* ============================================================
       4. Active aggregation
       ============================================================ */
    active_agg AS (
        SELECT
            dt,
            crown_level,
            count(distinct userid) as dau
        FROM da_with_crown
        WHERE rn = 1
        GROUP BY 1,2
    ),

    /* ============================================================
       5. Purchase
       ============================================================ */
    mysource AS (
        SELECT
            date_trunc('day',st.datecreated)::date AS dt,
            st.userid,
            case when extract(day from st.datecreated) >= 16
                then date_trunc('month',st.datecreated)::date + 15
                else date_trunc('month',st.datecreated)::date
            end as period_start_date,
            sum(st.amountdollars) as ext_revenue
        FROM store_transaction st
        CROSS JOIN params p
        LEFT JOIN public.users_data ud
            ON st.userid = ud.userid
        WHERE st.datecreated >= p.start_date
            AND st.datecreated < p.report_date + 1
            AND st.status in ('AUTHORIZED','COMPLETED')
            AND ud.terminated = 0
            AND st.amountdollars > 0
        GROUP BY 1,2,3
    ),

    /* ============================================================
       6. Payer + Crown
       ============================================================ */
    rev_agg AS (
        SELECT
            ms.dt,
            ms.userid,
            ms.ext_revenue,
            max(coalesce(cl.crown_level,0)) as crown_level
        FROM mysource ms
        LEFT JOIN cl
            ON cl.userid = ms.userid
            AND cl.dt >= ms.period_start_date
            AND cl.dt <= ms.dt
        GROUP BY 1,2,3
    ),

    /* ============================================================
       7. Paying aggregation
       ============================================================ */
    paying_agg AS (
        SELECT
            dt,
            crown_level,
            count(distinct userid) as payers,
            sum(ext_revenue) as revenue
        FROM rev_agg
        GROUP BY 1,2
    ),

    /* ============================================================
       8. Active + Paying
       ============================================================ */
    combined AS (
        SELECT
            coalesce(a.dt,p.dt) as dt,
            coalesce(a.crown_level,p.crown_level) as crown_level,
            coalesce(a.dau,0) as dau,
            coalesce(p.payers,0) as payers,
            coalesce(p.revenue,0) as revenue
        FROM active_agg a
        FULL OUTER JOIN paying_agg p
            ON a.dt = p.dt
            AND a.crown_level = p.crown_level
    ),

    /* ============================================================
       9. Wide → Long
       ============================================================ */
    daily_metrics AS (
        SELECT
            dt,
            crown_level,
            'dau' AS metric,
            dau::decimal(38,4) AS value
        FROM combined

        UNION ALL

        SELECT
            dt,
            crown_level,
            'payers' AS metric,
            payers::decimal(38,4) AS value
        FROM combined

        UNION ALL

        SELECT
            dt,
            crown_level,
            'revenue' AS metric,
            revenue::decimal(38,4) AS value
        FROM combined
    ),

    /* ============================================================
       10. Baseline + Comparison
       ============================================================ */
    metric_comparisons AS (
        SELECT
            dt,
            crown_level,
            metric,
            value,
            lag(value,1) over (
                partition by crown_level,metric
                order by dt
            ) as previous_day,
            lag(value,7) over (
                partition by crown_level,metric
                order by dt
            ) as last_week,
            round(avg(value) over (
                partition by crown_level,metric
                order by dt
                rows between 7 preceding and 1 preceding
            ),2) as previous_7d_avg,
            round(avg(value) over (
                partition by crown_level,metric
                order by dt
                rows between 30 preceding and 1 preceding
            ),2) as previous_30d_avg,
            round(stddev_samp(value) over (
                partition by crown_level,metric
                order by dt
                rows between 30 preceding and 1 preceding
            ),2) as previous_30d_stddev
        FROM daily_metrics
    )

SELECT
    m.*,
    round((value / nullif(previous_day,0) - 1) * 100,2) as dod_pct,
    round((value / nullif(last_week,0) - 1) * 100,2) as wow_pct,
    round((value / nullif(previous_7d_avg,0) - 1) * 100,2) as vs_7d_avg_pct,
    round((value / nullif(previous_30d_avg,0) - 1) * 100,2) as vs_30d_avg_pct,
    round((value - previous_30d_avg) / nullif(previous_30d_stddev,0),2) as z_score_30d
FROM metric_comparisons m
WHERE m.dt = (SELECT report_date FROM params)
ORDER BY m.crown_level,m.metric;