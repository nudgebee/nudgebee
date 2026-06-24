DELETE FROM messaging_platforms mp1
USING messaging_platforms mp2
WHERE mp1.tenant_id = mp2.tenant_id
  AND mp1.platform = mp2.platform
  AND mp1.id < mp2.id;

ALTER TABLE messaging_platforms
ADD CONSTRAINT uq_messaging_platforms_tenant_platform UNIQUE (tenant_id, platform);
