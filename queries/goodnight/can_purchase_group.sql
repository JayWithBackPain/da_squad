-- @description:罐頭的每日消費者百分位的儲值量能
-- @role:supporting
-- @supports:main_kpi

SELECT DISTINCT
    date,
    ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date),2) AS median_revenue,
    ROUND(PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date),2) AS p90_revenue,
    ROUND(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date),2) AS p99_revenue,
    ROUND(PERCENTILE_CONT(0.999) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date),2) AS p999_revenue,
    ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date),2) AS median_transactions,
    ROUND(PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date),2) AS p90_transactions,
    ROUND(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date),2) AS p99_transactions,
    ROUND(PERCENTILE_CONT(0.999) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date),2) AS p999_transactions
FROM (
    SELECT
    date_trunc('day', purchased_at)::DATE AS date,
    user_id,
    count(distinct ch_id)                 AS transactions,
    sum(usd_price)                        AS revenue
    FROM fact.can_purchase
    WHERE purchased_at >= {{run_date}} - 30
    and purchased_at < {{run_date}}
    GROUP BY 1, 2
    HAVING sum(usd_price) > 0
    ) mysource