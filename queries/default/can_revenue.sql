-- @description:用戶的罐頭儲值收入
-- @role: primary
select
    date_trunc('day',purchased_at) as dt,
    sum(usd_price) as can_revenue,
    count(distinct user_id) as payers,
    can_revenue::float / nullif(payers, 0) as arppu
from fact.can_purchase
where purchased_at >= current_date - 30
    and purchased_at < CURRENT_DATE
group by 1