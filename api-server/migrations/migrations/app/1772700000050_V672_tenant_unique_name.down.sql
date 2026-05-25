DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenant_name_key'
      AND conrelid = 'public.tenant'::regclass
  ) THEN
    ALTER TABLE "public"."tenant" DROP CONSTRAINT "tenant_name_key";
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenant_name_created_by_key'
      AND conrelid = 'public.tenant'::regclass
  ) THEN
    ALTER TABLE "public"."tenant" ADD CONSTRAINT "tenant_name_created_by_key" UNIQUE ("name", "created_by");
  END IF;
END $$;
