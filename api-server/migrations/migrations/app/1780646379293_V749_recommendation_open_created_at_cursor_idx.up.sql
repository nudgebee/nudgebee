-- Supports runbook-server WorkflowDao.FindNewRecommendations cursor pagination:
--   WHERE status = 'Open' AND created_at >= $1 [AND (created_at, id) > ($1, $3)]
--   ORDER BY created_at ASC, id ASC LIMIT 500
--
-- Before this index the planner used idx_status (status only), scanning every
-- 'Open' row (~178k) and then top-N sorting by (created_at, id) -> ~665ms.
-- A partial index keyed on (created_at, id) over just status='Open' rows turns
-- this into an ordered range scan that short-circuits at LIMIT (~1ms).
CREATE INDEX IF NOT EXISTS idx_recommendation_open_created_at_id
    ON public.recommendation (created_at, id)
    WHERE status = 'Open';
