select * from auto_pilot;

update
	auto_pilot as tb1
set
	rule = jsonb_set(
		tb1.rule,
		'{resource_filter,resource_id}',
		jsonb_build_object(
			'namespace',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resource_id' -> 'inclusions' ->> 0,
				'/',
				1
			),
			'type',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resource_id' -> 'inclusions' ->> 0,
				'/',
				2
			),
			'name',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resource_id' -> 'inclusions' ->> 0,
				'/',
				3
			)
		)
	)
FROM
	auto_pilot as tb2
where
	tb1.id = tb2.id
	and tb1.rule -> 'resource_filter' -> 'resource_id' -> 'inclusions' ->> 0 IS NOT null;

update
	auto_pilot as tb1
set
	rule = jsonb_set(
		tb1.rule,
		'{resource_filter,resource_id}',
		jsonb_build_object(
			'namespace',
			split_part(
				tb2.rule -> 'resource_filter' -> 'owner_resource_id' -> 'inclusions' ->> 0,
				'/',
				1
			),
			'type',
			split_part(
				tb2.rule -> 'resource_filter' -> 'owner_resource_id' -> 'inclusions' ->> 0,
				'/',
				2
			),
			'name',
			split_part(
				tb2.rule -> 'resource_filter' -> 'owner_resource_id' -> 'inclusions' ->> 0,
				'/',
				3
			)
		)
	)
FROM
	auto_pilot as tb2
where
	tb1.id = tb2.id
	and tb1.rule -> 'resource_filter' -> 'owner_resource_id' -> 'inclusions' ->> 0 IS NOT null;

UPDATE
	auto_pilot
SET
	rule = jsonb_set(
		rule,
		'{resource_filter}',
		(rule -> 'resource_filter') :: jsonb - 'owner_resource_id'
	)
WHERE
	rule -> 'resource_filter' -> 'owner_resource_id' IS NOT NULL;

update
	auto_pilot as tb1
set
	rule = jsonb_set(
		tb1.rule,
		'{resource_filter,resource_id}',
		jsonb_build_object(
			'namespace',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resourse_id' -> 'inclusions' ->> 0,
				'/',
				1
			),
			'type',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resourse_id' -> 'inclusions' ->> 0,
				'/',
				2
			),
			'name',
			split_part(
				tb2.rule -> 'resource_filter' -> 'resourse_id' -> 'inclusions' ->> 0,
				'/',
				3
			)
		)
	)
FROM
	auto_pilot as tb2
where
	tb1.id = tb2.id
	and tb1.rule -> 'resource_filter' -> 'resourse_id' -> 'inclusions' ->> 0 IS NOT null;

UPDATE
	auto_pilot
SET
	rule = jsonb_set(
		rule,
		'{resource_filter}',
		(rule -> 'resource_filter') :: jsonb - 'resourse_id'
	)
WHERE
	rule -> 'resource_filter' -> 'resourse_id' IS NOT NULL;

update
	auto_pilot as tb1
set
	rule = jsonb_set(
		tb1.rule,
		'{resource_filter,resource_id}',
		jsonb_build_object(
			'namespace',
			tb2.rule -> 'resource_filter' -> 'namespace' -> 'inclusions' ->> 0,
			'type',
			null,
			'name',
			null
		)
	)
FROM
	auto_pilot as tb2
where
	tb1.id = tb2.id
	and tb1.rule -> 'resource_filter' -> 'namespace' -> 'inclusions' ->> 0 IS NOT null;

UPDATE
	auto_pilot
SET
	rule = jsonb_set(
		rule,
		'{resource_filter}',
		(rule -> 'resource_filter') :: jsonb - 'namespace'
	)
WHERE
	rule -> 'resource_filter' -> 'namespace' IS NOT NULL;

delete from
	auto_pilot_task
where
	auto_pilot_id in (
		select
			id
		from
			auto_pilot
		WHERE
			rule :: varchar like '%Pod%'
	);

delete from
	auto_pilot
WHERE
	rule :: varchar like '%Pod%';

INSERT INTO
	auto_optimize_resource_map (
		resource_identifier,
		auto_optimize_type,
		auto_optimize_id,
		tenant_id,
		account_id
	)
SELECT
	(rule -> 'resource_filter' -> 'resource_id') :: jsonb,
	category,
	id,
	tenant_id,
	account_id
FROM
	auto_pilot
WHERE
	rule -> 'resource_filter' -> 'resource_id' is not null
	and id not in (
		select
			distinct auto_optimize_id
		from
			auto_optimize_resource_map
	);

UPDATE
	auto_pilot
SET
	rule = jsonb_set(
		rule,
		'{resource_filter}',
		(rule -> 'resource_filter') :: jsonb - 'namespace'
	)
WHERE
	rule -> 'resource_filter' -> 'namespace' IS NOT NULL;

update
	auto_pilot
set
	rule = (rule - 'resource_filter') :: jsonb
where
	rule -> 'resource_filter' is not null;

update
	auto_pilot
set
	rule = (rule -> 'rules') :: jsonb
where
	rule -> 'rules' is not null