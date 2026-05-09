


CREATE INDEX IF NOT EXISTS idx_llm_conv_tool_calls_conv_id_tool_type                                                                                                                                   
  ON llm_conversation_tool_calls (conversation_id, tool_type);

CREATE INDEX IF NOT EXISTS idx_llm_conv_agent_conv_id                                                                                                                                                  
 ON llm_conversation_agent (conversation_id);
