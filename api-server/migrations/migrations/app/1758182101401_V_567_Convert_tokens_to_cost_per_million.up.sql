
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns 
             WHERE table_schema = 'public' 
             AND table_name = 'llm_model_pricing' 
             AND column_name = 'cost_per_input_token') THEN
    ALTER TABLE "public"."llm_model_pricing" RENAME COLUMN "cost_per_input_token" TO "cost_per_million_input_tokens";
    COMMENT ON COLUMN "public"."llm_model_pricing"."cost_per_million_input_tokens" IS 'in USD($)';
  END IF;
  
  IF EXISTS (SELECT 1 FROM information_schema.columns 
             WHERE table_schema = 'public' 
             AND table_name = 'llm_model_pricing' 
             AND column_name = 'cost_per_output_token') THEN
    COMMENT ON COLUMN "public"."llm_model_pricing"."cost_per_output_token" IS 'in USD($)';
    ALTER TABLE "public"."llm_model_pricing" RENAME COLUMN "cost_per_output_token" TO "cost_per_million_output_tokens";
  END IF;
END $$;
