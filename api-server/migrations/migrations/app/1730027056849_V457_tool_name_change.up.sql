update llm_conversation_agent 
set agent_name = 'prometheus'
where agent_name  = 'queryPrometheus';

update llm_conversation_agent 
set agent_name = 'promql'
where agent_name  in ('generatePrometheusQuery', 'generate_prometheus_query', 'prometheus_query');

update llm_conversation_agent 
set agent_name = 'logs'
where agent_name  in ('queryLog');

update llm_conversation_agent 
set agent_name = 'kubectl_log'
where agent_name  in ('kubectlLog');

update llm_conversation_agent 
set agent_name = 'logql'
where agent_name  in ('loki_query');

update llm_conversation_agent 
set agent_name = 'loki'
where agent_name  in ('queryLoki');

update llm_conversation_agent 
set agent_name = 'traces'
where agent_name  in ('queryTraces');

update llm_conversation_agent 
set agent_name = 'events'
where agent_name  in ('queryEvents', 'executeEventsSql');

update llm_conversation_agent 
set agent_name = 'planner'
where agent_name  in ('k8s-troubleshooting-steps-planner');

update llm_conversation_agent 
set agent_name = 'kubectl'
where agent_name  in ('k8s', 'KubectlExecutor');

update llm_conversation_agent 
set agent_name = 'postgres_debug'
where agent_name  in ('queryPostgres', 'PostgresQueryExecutor');

update llm_conversation_agent 
set agent_name = 'recommendations'
where agent_name  in ('executeRecommendationSql');


