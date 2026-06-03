CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_task_image_scanner_unique
ON public.agent_task USING btree (cloud_account_id, ((payload -> 'action_params' ->> 'image_name')))
WHERE (action = 'image_scanner');
