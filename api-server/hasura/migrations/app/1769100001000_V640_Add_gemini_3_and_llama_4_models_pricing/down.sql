DELETE FROM llm_model_pricing 
WHERE model_name IN (
    'gemini-3-pro-preview', 
    'gemini-3-flash-preview',
    'meta.llama4-maverick-17b-instruct-v1:0',
    'us.meta.llama4-maverick-17b-instruct-v1:0',
    'meta.llama4-scout-17b-instruct-v1:0',
    'us.meta.llama4-scout-17b-instruct-v1:0',
    'arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama4-maverick-17b-instruct-v1:0',
    'arn:aws:bedrock:us-west-2:864186153326:inference-profile/us.meta.llama4-scout-17b-instruct-v1:0'
) 
AND provider_name IN ('googleai', 'bedrock');