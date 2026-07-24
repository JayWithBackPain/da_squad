-- @desc: 近 30 日全球每日 DAU 時間序列
-- @role: primary
select
    dt as date,
    count(distinct user_id) as dau
from datamart.daily_user_activities
where dt >= current_Date - 30
    and dt < CURRENT_DATE
group by 1
