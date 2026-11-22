-- llm-server refuses the SQL tools once the role or schema is gone, unless
-- LLM_SERVER_AGENT_SQL_SCOPED_ROLE=false. The role is left in place: other databases on the
-- same server may still use it, and it owns nothing.
DROP SCHEMA IF EXISTS nb_llm_tools CASCADE;
