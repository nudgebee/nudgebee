INSERT INTO "public"."feature"("description", "value")
VALUES (E'Enable LLM-based subject resolution for webhook alerts', E'WEBHOOK_LLM_RESOLUTION')
ON CONFLICT (value) DO NOTHING;
