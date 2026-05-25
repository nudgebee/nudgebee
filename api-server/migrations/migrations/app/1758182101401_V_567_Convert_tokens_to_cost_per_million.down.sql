
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns 
             WHERE table_schema = 'public' 
             AND table_name = 'llm_model_pricing' 
             AND column_name = 'cost_per_million_output_tokens') THEN
    ALTER TABLE "public"."llm_model_pricing" RENAME COLUMN "cost_per_million_output_tokens" TO "cost_per_output_token";
    COMMENT ON COLUMN "public"."llm_model_pricing"."cost_per_output_token" IS NULL;
  END IF;
  
  IF EXISTS (SELECT 1 FROM information_schema.columns 
             WHERE table_schema = 'public' 
             AND table_name = 'llm_model_pricing' 
             AND column_name = 'cost_per_million_input_tokens') THEN
    COMMENT ON COLUMN "public"."llm_model_pricing"."cost_per_million_input_tokens" IS NULL;
    ALTER TABLE "public"."llm_model_pricing" RENAME COLUMN "cost_per_million_input_tokens" TO "cost_per_input_token";
  END IF;
END $$;
