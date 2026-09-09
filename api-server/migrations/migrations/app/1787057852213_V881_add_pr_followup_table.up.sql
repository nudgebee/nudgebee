-- Fixes #36457: PR followup is currently keyed on the resolution row
-- (event_resolution / recommendation_resolution), not the pull request, so
-- one PR with resolution rows in both tables runs two independent followup
-- loops with separate iteration counters and separate verdicts. This table
-- becomes the single claim/iteration-counter/lifecycle-state per PR URL;
-- resolution rows remain the durable metadata + terminal-state record.
--
-- Empty table at creation time (no data migration), so this is a cheap,
-- effectively instant DDL — no lock-window concern.
CREATE TABLE "public"."pr_followup" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "pr_url" text NOT NULL,
    "tenant_id" text NOT NULL,
    "pr_lifecycle_state" text NOT NULL DEFAULT 'created',
    "pr_iteration_count" integer NOT NULL DEFAULT 0,
    "last_pr_check_at" timestamptz,
    "pr_followup_pending" boolean NOT NULL DEFAULT false,
    "status_message" text,
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "pr_followup_url_unique" UNIQUE ("pr_url")
);

CREATE INDEX "idx_pr_followup_state_check" ON "public"."pr_followup" ("pr_lifecycle_state", "last_pr_check_at");
