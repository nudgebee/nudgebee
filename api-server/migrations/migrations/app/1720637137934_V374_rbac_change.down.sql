alter table user_roles
	ALTER COLUMN entity_id TYPE uuid USING entity_id::uuid;

alter table group_roles 
	alter column entity_id type uuid USING entity_id::uuid;