ALTER TABLE "public"."event_triage_rules" ADD COLUMN IF NOT EXISTS "apply_to_existing" boolean NOT NULL DEFAULT false;
