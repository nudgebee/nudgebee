
create table if not exists "public"."tenant_onboarding" (
	"id" uuid not null default gen_random_uuid(),
	"created_at" timestamp not null default now(),
	"updated_at" timestamp not null default now(),
	"username" text not null,
	"verification_token" text not null,
	"verification_token_expiration" timestamp not null,
	"verification_status" text not null default 'pending',
	primary key ("id") ,
	unique ("username"),
	unique("verification_token"), 
	constraint "verification_status_check" check (verification_status in ('pending', 'done', 'expired'))
);
CREATE EXTENSION IF NOT EXISTS pgcrypto;
