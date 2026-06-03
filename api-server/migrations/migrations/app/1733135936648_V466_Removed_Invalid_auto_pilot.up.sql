-- Create a temporary table
CREATE TABLE temp_result AS
SELECT auto_optimize_id 
FROM auto_optimize_resource_map 
where (resource_identifier ->> 'name' is null and resource_identifier ->> 'namespace' is null) or (resource_identifier ->> 'name' is not null and  resource_identifier ->> 'type' is null and   resource_identifier ->> 'namespace' is not null) or (resource_identifier ->> 'name' is null and  resource_identifier ->> 'type' is not null and   resource_identifier ->> 'namespace' is not null);

DELETE 
FROM auto_pilot_task 
WHERE auto_pilot_id in (SELECT auto_optimize_id FROM temp_result);

DELETE 
FROM auto_optimize_resource_map aorm 
WHERE auto_optimize_id in (SELECT auto_optimize_id FROM temp_result);


DELETE 
FROM auto_pilot 
WHERE id in (SELECT auto_optimize_id FROM temp_result);

DROP TABLE temp_result;
