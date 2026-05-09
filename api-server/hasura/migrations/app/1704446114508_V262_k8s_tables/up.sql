

CREATE TABLE "public"."k8s_pods" ("tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "cloud_resource_id" uuid NOT NULL, "external_id" text NOT NULL, "name" text NOT NULL, "workload_type" text NOT NULL, "workload_name" text NOT NULL, "namespace" text NOT NULL, "status" text NOT NULL, "node_name" text NOT NULL, "is_active" boolean NOT NULL, "restart_count" jsonb, "creation_time" timestamp NOT NULL, "last_seen" timestamp NOT NULL, "labels" jsonb, "meta" jsonb, PRIMARY KEY ("cloud_resource_id","tenant_id","cloud_account_id") , UNIQUE ("tenant_id", "cloud_resource_id", "cloud_account_id"));

CREATE TABLE "public"."k8s_workloads" ("tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "cloud_resource_id" uuid NOT NULL, "external_id" text NOT NULL, "namespace" text NOT NULL, "is_active" boolean NOT NULL, "total_pods" integer NOT NULL, "ready_pods" integer NOT NULL, "name" text NOT NULL, "kind" text NOT NULL, "creation_time" timestamp NOT NULL, "last_seen" timestamp NOT NULL, "labels" jsonb NOT NULL, "meta" jsonb NOT NULL, PRIMARY KEY ("tenant_id","cloud_account_id","cloud_resource_id") , UNIQUE ("tenant_id", "cloud_account_id", "cloud_resource_id"));

CREATE TABLE "public"."k8s_nodes" ("tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "name" text NOT NULL, "is_active" boolean NOT NULL, "node_creation_time" timestamp, "conditions" text, "node_type" text, "node_flavor" text, "node_region" text, "node_zone" text, "memory_capacity" integer NOT NULL, "cpu_capacity" integer NOT NULL, "memory_allocatable" integer NOT NULL, "cpu_allocatable" integer NOT NULL, "meta" jsonb, "cloud_resource_id" uuid NOT NULL, PRIMARY KEY ("tenant_id","cloud_account_id","cloud_resource_id") , UNIQUE ("tenant_id", "cloud_account_id", "cloud_resource_id"));

CREATE TABLE "public"."k8s_namespace" ("name" text NOT NULL, "tenant_id" text NOT NULL, "cloud_account_id" text NOT NULL, "is_active" boolean NOT NULL, PRIMARY KEY ("name","tenant_id","cloud_account_id") , UNIQUE ("name", "tenant_id", "cloud_account_id"));

alter table "public"."k8s_namespace" rename to "k8s_namespaces";

alter table "public"."k8s_nodes" add column "external_ip" text
 null;

alter table "public"."k8s_nodes" add column "internal_ip" text
 null;

alter table "public"."k8s_nodes" add column "labels" jsonb
 null;

alter table "public"."k8s_nodes" add column "taints" text
 null;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE float8;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE int4;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_capacity" TYPE float8;

ALTER TABLE "public"."k8s_nodes" ALTER COLUMN "cpu_allocatable" TYPE float8;

alter table "public"."spends" add column "tags" jsonb
 null;
