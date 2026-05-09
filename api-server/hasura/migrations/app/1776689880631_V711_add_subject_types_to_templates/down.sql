-- Remove subject_types key from all system templates
UPDATE workflow_templates SET tags = tags - 'subject_types'
WHERE is_system = true AND tenant_id IS NULL AND tags ? 'subject_types';
