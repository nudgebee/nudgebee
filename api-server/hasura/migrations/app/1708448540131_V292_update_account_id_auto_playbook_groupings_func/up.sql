
DROP FUNCTION auto_playbook_groupings;
CREATE OR REPLACE FUNCTION public.auto_playbook_groupings(playbook_account_id text, playbook_status text DEFAULT NULL::text, playbook_name text DEFAULT NULL::text, type_filter text DEFAULT NULL::text, name_filter text DEFAULT NULL::text, hasura_session json DEFAULT '{}'::json)
 RETURNS SETOF auto_playbook_groupings_type
 LANGUAGE sql
 STABLE
AS $function$
    SELECT count(*)
    FROM auto_playbook ap
    WHERE
        (
            "hasura_session" ->> 'x-hasura-user-tenant-id' IS NULL
            OR ap."tenant_id" = ("hasura_session" ->> 'x-hasura-user-tenant-id')::uuid
        )
        AND
        (ap.account_id = playbook_account_id :: uuid) AND
        (playbook_status IS NULL OR status = playbook_status) AND
        (playbook_name IS NULL OR name LIKE '%' || playbook_name || '%') AND
        (type_filter IS NULL OR trigger->'event'->>'type' = type_filter)
        AND
        (name_filter IS NULL OR EXISTS (
            SELECT 1
            FROM jsonb_array_elements(resource_filter) AS rf
            WHERE rf->>'name' = name_filter
        ));
$function$;
