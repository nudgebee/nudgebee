CREATE OR REPLACE FUNCTION public.get_event_resolution_resolver_user(er event_resolution)
RETURNS SETOF users
LANGUAGE sql
STABLE
AS $$
  SELECT u.*
  FROM users u
  WHERE er.resolver_type = 'User'
    AND u.id = er.resolver_id::uuid;
$$;
