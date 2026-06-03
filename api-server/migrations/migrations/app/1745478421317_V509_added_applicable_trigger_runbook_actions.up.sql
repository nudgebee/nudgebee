UPDATE runbook_action
SET attributes = jsonb_set(attributes, '{applicable_trigger}', '[]'::jsonb) where internal_identifier is not null;

UPDATE runbook_action
SET attributes = jsonb_set(attributes, '{applicable_trigger}', '["event"]'::jsonb) where internal_identifier = 'pv_rightsize';
