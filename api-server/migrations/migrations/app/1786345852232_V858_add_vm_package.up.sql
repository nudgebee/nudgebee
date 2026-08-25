CREATE TABLE IF NOT EXISTS "public"."vm_package" (
    "id" uuid NOT NULL DEFAULT gen_random_uuid(),
    "tenant_id" uuid NOT NULL,
    "cloud_account_id" uuid NOT NULL,
    "cloud_resource_id" uuid NOT NULL,
    "os_family" text NOT NULL,
    "os_version" text NOT NULL,
    "pkg_type" text NOT NULL,
    "name" text NOT NULL,
    "version" text NOT NULL,
    "arch" text NOT NULL DEFAULT '',
    -- Nullable on purpose: rpm's epoch is genuinely absent ("(none)") on most
    -- packages, distinct from an explicit epoch of 0. Never coerce to 0 —
    -- vuln-matcher-server's Epoch *int carries this same nil/0 distinction.
    "epoch" integer,
    -- epoch_key exists only so ON CONFLICT has a NOT NULL identity column:
    -- Postgres unique constraints never consider two NULLs equal, so two
    -- "(none)"-epoch rows for the same package would never conflict on a raw
    -- epoch column and would silently duplicate on every re-scan.
    "epoch_key" integer NOT NULL GENERATED ALWAYS AS (COALESCE("epoch", -1)) STORED,
    "source_name" text NOT NULL DEFAULT '',
    "source_version" text NOT NULL DEFAULT '',
    "is_active" boolean NOT NULL DEFAULT true,
    "first_seen_at" timestamp NOT NULL DEFAULT now(),
    "last_seen_at" timestamp NOT NULL DEFAULT now(),
    "created_at" timestamp NOT NULL DEFAULT now(),
    "updated_at" timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY ("id"),
    CONSTRAINT "vm_package_tenant_id_fkey" FOREIGN KEY ("tenant_id") REFERENCES "public"."tenant"("id") ON UPDATE restrict ON DELETE cascade,
    CONSTRAINT "vm_package_cloud_account_id_fkey" FOREIGN KEY ("cloud_account_id") REFERENCES "public"."cloud_accounts"("id") ON UPDATE restrict ON DELETE cascade,
    CONSTRAINT "vm_package_cloud_resource_id_fkey" FOREIGN KEY ("cloud_resource_id") REFERENCES "public"."cloud_resourses"("id") ON UPDATE restrict ON DELETE cascade
);

CREATE UNIQUE INDEX IF NOT EXISTS "uq_vm_package_identity"
    ON "public"."vm_package" ("cloud_resource_id", "pkg_type", "name", "version", "arch", "epoch_key", "source_name");

CREATE INDEX IF NOT EXISTS "idx_vm_package_resource_active" ON "public"."vm_package" ("cloud_resource_id") WHERE "is_active";

CREATE INDEX IF NOT EXISTS "idx_vm_package_tenant_account" ON "public"."vm_package" ("tenant_id", "cloud_account_id");

DROP TRIGGER IF EXISTS "set_public_vm_package_updated_at" ON "public"."vm_package";
CREATE TRIGGER "set_public_vm_package_updated_at"
BEFORE UPDATE ON "public"."vm_package"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_vm_package_updated_at" ON "public"."vm_package"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
