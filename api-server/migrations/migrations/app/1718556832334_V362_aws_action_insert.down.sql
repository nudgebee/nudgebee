
DELETE FROM "public"."runbook_action" WHERE "internal_identifier" = 'aws_instance_start' and 'library_id' = '0e1e7e4c-b09c-4259-81dc-aec51ef45114';

DELETE FROM "public"."runbook_action" WHERE "internal_identifier" = 'aws_instance_stop' and 'library_id' = '0e1e7e4c-b09c-4259-81dc-aec51ef45114';

DELETE FROM "public"."runbook_action" WHERE "internal_identifier" = 'aws_rds_instance_stop' and 'library_id' = '0e1e7e4c-b09c-4259-81dc-aec51ef45114';

DELETE FROM "public"."runbook_action" WHERE "internal_identifier" = 'aws_rds_instance_start' and 'library_id' = '0e1e7e4c-b09c-4259-81dc-aec51ef45114';
