-- workflows.bee binds an automation to an agent: {bee, install_id, name, role, mode}.
-- role "automation" is an automation only its agent may start (an extension
-- decides on every run whether it may run); role "trigger" is the hidden
-- automation that starts the agent (kept out of automation lists). Written only
-- by an extension's install path, never by the generic create/update API. NULL
-- for every other automation, and always NULL without the extension.
-- Lock window: ADD COLUMN with no default is a metadata-only change.
ALTER TABLE public.workflows ADD COLUMN IF NOT EXISTS bee jsonb;
