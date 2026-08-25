-- Revert V869: drop the secret_env_exposure rule metadata row.
DELETE FROM public.recommendation_rule
WHERE rule_name = 'secret_env_exposure' AND cloud_provider IS NULL;
