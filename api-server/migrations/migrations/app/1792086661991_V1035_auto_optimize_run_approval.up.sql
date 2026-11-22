-- One auto_pilot_approvals row per gated auto optimize run. The row is filed
-- before anyone reviews it, and the same reviewer can decide many runs of one
-- automation, so reviewer_id and policy_id become optional and the per-reviewer
-- unique key goes. The table is small; every statement here is a brief lock.
ALTER TABLE public.auto_pilot_approvals ALTER COLUMN reviewer_id DROP NOT NULL;
ALTER TABLE public.auto_pilot_approvals ALTER COLUMN policy_id DROP NOT NULL;
ALTER TABLE public.auto_pilot_approvals
    DROP CONSTRAINT IF EXISTS auto_pilot_approvals_autopilot_id_reviewer_id_account_id_tenant_id_key;

INSERT INTO public.auto_pilot_approval_status (status, description) VALUES
    ('EXPIRED', 'the approval was not decided in time'),
    ('CANCELLED', 'the automation changed or was disabled before the approval was decided')
ON CONFLICT (status) DO NOTHING;

INSERT INTO public.auto_pilot_task_status (value, description) VALUES
    ('Pending_Approval', 'the task is planned and waits for its run to be approved')
ON CONFLICT (value) DO NOTHING;

CREATE INDEX IF NOT EXISTS auto_pilot_approvals_autopilot_id_status_idx
    ON public.auto_pilot_approvals (autopilot_id, status);
