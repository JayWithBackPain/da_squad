-- @description:日報用的主要指標+週期性對照參考

SELECT DISTINCT
    date,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date) AS median_revenue,
    PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date) AS p90_revenue,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY revenue) OVER (PARTITION BY date) AS p99_revenue,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date) AS median_transactions,
    PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date) AS p90_transactions,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY transactions) OVER (PARTITION BY date) AS p99_transactions
FROM (
         SELECT
             date_trunc('day', purchased_at)::DATE AS date,
             user_id,
             count(distinct ch_id)                 AS transactions,
             sum(usd_price)                        AS revenue
         FROM fact.can_purchase
         WHERE purchased_at >= CURRENT_DATE - 30
            and purchased_at < CURRENT_DATE
         GROUP BY 1, 2
         HAVING sum(usd_price) > 0
     ) mysource