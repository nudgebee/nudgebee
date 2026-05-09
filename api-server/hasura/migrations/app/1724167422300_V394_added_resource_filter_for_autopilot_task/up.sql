UPDATE
    auto_pilot_task
SET
    resource_filter = temp_2.resource_filter
FROM
    (
        select
            json_build_object(
                'namespace',
                result [1],
                'type',
                result [2],
                'name',
                result [3]
            ) as resource_filter,
            task_id
        from
            (
                select
                    regexp_split_to_array(cr.resourse_id, '/') as result,
                    t.id as task_id
                from
                    cloud_resourses cr
                    join recommendation r on cr.id = r.resource_id
                    join auto_pilot_task t on t.recommendation_id = r.id
                where
                    t.resource_filter = '{}'
            ) as temp
    ) as temp_2
WHERE
    auto_pilot_task.id = temp_2.task_id;
