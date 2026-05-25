-- Step 1: Rename duplicate tenant names by appending a suffix (e.g. "MyTenant" -> "MyTenant-2")
WITH duplicates AS (
  SELECT id, name,
    ROW_NUMBER() OVER (PARTITION BY name ORDER BY created_at) AS rn
  FROM "public"."tenant"
)
UPDATE "public"."tenant"
SET name = duplicates.name || '-' || duplicates.rn
FROM duplicates
WHERE "tenant".id = duplicates.id AND duplicates.rn > 1;

-- Step 2: Drop the existing composite unique constraint (if it exists)
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenant_name_created_by_key'
      AND conrelid = 'public.tenant'::regclass
  ) THEN
    ALTER TABLE "public"."tenant" DROP CONSTRAINT "tenant_name_created_by_key";
  END IF;
END $$;

-- Step 3: Add unique constraint on name only (if not already present)
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenant_name_key'
      AND conrelid = 'public.tenant'::regclass
  ) THEN
    ALTER TABLE "public"."tenant" ADD CONSTRAINT "tenant_name_key" UNIQUE ("name");
  END IF;
END $$;
