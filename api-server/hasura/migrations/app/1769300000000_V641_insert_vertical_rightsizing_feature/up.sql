INSERT INTO "public"."feature"("description", "value")
VALUES (E'Enable vertical rightsizing recommendations for K8s workloads', E'VERTICAL_RIGHTSIZING')
ON CONFLICT (value) DO NOTHING;
