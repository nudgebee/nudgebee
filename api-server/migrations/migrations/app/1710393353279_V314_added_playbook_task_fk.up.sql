
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM information_schema.table_constraints
        WHERE constraint_type = 'FOREIGN KEY'
        AND constraint_name = 'auto_playbook_task_status_fkey'
    ) THEN
        ALTER TABLE public.auto_playbook_task
        ADD CONSTRAINT auto_playbook_task_status_fkey
        FOREIGN KEY (status)
        REFERENCES public.auto_playbook_task_status (value)
        ON UPDATE RESTRICT
        ON DELETE RESTRICT;
    END IF;
END $$;
