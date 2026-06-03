update
	agent_task
set
	source = 'auto_optimize'
where
	source = 'autopilot';

update
	recommendation
set
	dismissed_reason = 'Executed by auto optimize'
where
	dismissed_reason = 'Executed by autopilot'

