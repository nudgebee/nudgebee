update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,resource_applicable}','true');

update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,context_applicable}','false') where internal_identifier in  ('aws_instance_scalar','aws_eks_scalar','aws_rds_instance_scalar');

update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,resource_applicable}','false') where internal_identifier in  ('notification','ticket_create');

update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,resource_mandatory}','false') where internal_identifier in  ('notification','ticket_create');

update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,multiple_resource}','false') where internal_identifier in  ('notification','ticket_create');

update runbook_action set attributes = jsonb_set(attributes, '{resource_filter,multiple_resource}','false') where attributes -> 'resource_filter' ->> 'multiple_resource' is null;
