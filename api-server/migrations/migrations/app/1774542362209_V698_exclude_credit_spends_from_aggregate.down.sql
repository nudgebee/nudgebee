UPDATE spends SET exclude_aggregate = false WHERE amount < 0 AND exclude_aggregate = true;
