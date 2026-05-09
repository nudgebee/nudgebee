UPDATE spends SET exclude_aggregate = true WHERE amount < 0 AND exclude_aggregate = false;
