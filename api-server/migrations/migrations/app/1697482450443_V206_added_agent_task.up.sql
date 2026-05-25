
CREATE UNIQUE INDEX cloudaccount_k8s_resource_score_aggregate_pk ON public.cloudaccount_k8s_resource_score_aggregate USING btree (tenant, cloud_account_id);

CREATE TABLE "public"."agent_task" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "created_at" timestamptz NOT NULL DEFAULT now(), "updated_at" timestamptz NOT NULL DEFAULT now(), "cloud_account_id" uuid NOT NULL, "tenant" uuid NOT NULL, "action" text NOT NULL, "payload" jsonb NOT NULL, "status" text, "created_by" uuid, PRIMARY KEY ("id") , FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_agent_task_updated_at"
BEFORE UPDATE ON "public"."agent_task"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_agent_task_updated_at" ON "public"."agent_task"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
CREATE EXTENSION IF NOT EXISTS pgcrypto;
