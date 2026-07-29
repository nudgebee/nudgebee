-- Reverse of V776. Drops the NB AI Gateway tables (indexes go with them).
DROP TABLE IF EXISTS llm_gateway_rate_limits;
DROP TABLE IF EXISTS llm_gateway_routing_rules;
DROP TABLE IF EXISTS llm_gateway_request_log;
DROP TABLE IF EXISTS llm_gateway_usage;
