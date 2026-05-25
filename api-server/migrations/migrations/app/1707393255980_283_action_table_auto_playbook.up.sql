CREATE TABLE IF NOT EXISTS "public"."auto_playbook_actions" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "created_at" timestamp NOT NULL DEFAULT now(),
    "updated_at" timestamp NOT NULL DEFAULT now(),
    "name" text NOT NULL,
    "description" text NOT NULL,
    "category" text NOT NULL,
    "Source" text NOT NULL,
    "config" jsonb NOT NULL DEFAULT jsonb_build_object(),
    PRIMARY KEY ("id"),
    UNIQUE ("id"),
    UNIQUE ("name")
);

CREATE EXTENSION IF NOT EXISTS pgcrypto;