update
	recommendation_resolution
set
	type_reference_id = replace(type_reference_id,
	'autopilot/task',
	'auto-pilot/task')
where
	type_reference_id ilike '%autopilot/task%';
