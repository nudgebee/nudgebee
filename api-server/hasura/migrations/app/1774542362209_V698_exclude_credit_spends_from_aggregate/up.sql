-- Mark credit/refund spend rows as excluded from aggregation.
-- These rows have negative amounts (credits from cloud providers like GCP CUDs).
-- Excluding them prevents negative net costs in cost trend charts and aggregations.
UPDATE spends SET exclude_aggregate = true WHERE amount < 0 AND exclude_aggregate = false;
