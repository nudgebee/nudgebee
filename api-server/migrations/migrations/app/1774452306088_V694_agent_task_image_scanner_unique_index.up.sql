-- Delete duplicate image_scanner tasks, keeping the earliest one per (cloud_account_id, image_name)
DELETE FROM agent_task
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY cloud_account_id, payload->'action_params'->>'image_name'
                   ORDER BY created_at ASC
               ) AS rn
        FROM agent_task
        WHERE action = 'image_scanner'
    ) dupes
    WHERE rn > 1
);

CREATE UNIQUE INDEX idx_agent_task_image_scanner_unique
ON agent_task (cloud_account_id, (payload->'action_params'->>'image_name'))
WHERE action = 'image_scanner';
