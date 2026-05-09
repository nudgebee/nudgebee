UPDATE events
SET subject_type = LOWER(subject_type)
WHERE subject_type IN ('Deployment', 'StatefulSet', 'DaemonSet', 'Service', 'Job');
