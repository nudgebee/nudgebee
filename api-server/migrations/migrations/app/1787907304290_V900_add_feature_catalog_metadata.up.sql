-- Give public.feature the fields the tenant settings screen needs: a readable
-- name, a group, and what the feature does when a tenant has made no choice.
-- Until now the screen printed the raw flag id and used the description column
-- as a checkbox label.
--
-- Column order below is deliberate: create the lookup, add the columns
-- NULLable, backfill, then constrain. Adding them NOT NULL up front would fail
-- against existing rows.

-- Remove VERTICAL_RIGHTSIZING. The flag was default-ON and no tenant had it
-- disabled, so both the container-sizing cron and the disk-sizing trigger it
-- gated already ran for everyone. Dropping it changes behaviour for no one.
--
-- Order matters: feature_flag.feature_id references feature.value with
-- ON DELETE RESTRICT, so the tenant rows have to go first or the DELETE below
-- fails and takes the migration Job with it. Those rows all read 'enabled',
-- which for a default-ON flag asserted a state that was already true.
DELETE FROM public.feature_flag WHERE feature_id = 'VERTICAL_RIGHTSIZING';

DELETE FROM public.feature WHERE value = 'VERTICAL_RIGHTSIZING';

CREATE TABLE IF NOT EXISTS public.feature_category (
    id         text    PRIMARY KEY,
    label      text    NOT NULL,
    sort_order integer NOT NULL
);

INSERT INTO public.feature_category (id, label, sort_order) VALUES
    ('events',     'AI investigation & events', 10),
    ('assistant',  'AI assistant',              20),
    ('cost',       'Cost & optimisation',       30),
    ('monitoring', 'Monitoring & detection',    40),
    ('access',     'Access & platform',         50),
    ('other',      'Other',                    999)
ON CONFLICT (id) DO NOTHING;

ALTER TABLE public.feature
    ADD COLUMN IF NOT EXISTS display_name          text,
    ADD COLUMN IF NOT EXISTS category              text,
    ADD COLUMN IF NOT EXISTS polarity              text,
    ADD COLUMN IF NOT EXISTS stored_value_inverted boolean NOT NULL DEFAULT false;

-- polarity is what a tenant with NO feature_flag row gets. It is derivable from
-- the reader each call site uses -- IsFeatureEnabledByDefault* answers true
-- with no row, every other reader answers false -- so these values were read
-- off the call sites rather than chosen.

-- AI investigation & events
UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='Root cause analysis for events',
       description='Adds a root-cause panel to each event, explaining why it happened rather than only what fired.'
 WHERE value='GENERATE_RCA';

UPDATE public.feature SET category='events', polarity='default_on',
       display_name='Automatic event analysis',
       description='Analyses every event with AI as soon as it arrives, without anyone asking for it. Off means events still come in, but nothing is analysed automatically.'
 WHERE value='EVENT_AUTO_AI_SUMMARY';

-- The only flag whose stored value runs backwards: status='enabled' turns the
-- deeper pass OFF. Renaming it would mean rewriting live tenant rows, so the
-- name stays and the screen flips it instead. No row still means the pass runs,
-- hence polarity='default_on'.
UPDATE public.feature SET category='events', polarity='default_on', stored_value_inverted=true,
       display_name='Deep event analysis',
       description='Runs a deeper diagnostic pass on each event, on top of the standard analysis.'
 WHERE value='EVENT_DEBUG_ANALYSIS_DISABLED';

UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='Investigate events without service labels',
       description='Investigates events even when they carry no service label, instead of skipping them.'
 WHERE value='EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK';

UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='Auto-raise fix PRs',
       description='Opens a pull request with a suggested fix when event analysis traces a failure to a line of code.'
 WHERE value='EVENT_AUTO_RAISE_PR_ENABLED';

UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='Post event analysis to chat',
       description='Posts each event''s analysis into the incident''s chat channel as it completes.'
 WHERE value='EVENT_ANALYSIS_ON_CHANNEL';

UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='AI alert prioritisation',
       description='Sorts incoming alerts into P0-P3 by reading what they say, instead of the older severity formula.'
 WHERE value='TRIAGE_LLM_SCORING';

UPDATE public.feature SET category='events', polarity='opt_in',
       display_name='Troubleshooting',
       description='AI incident investigation. Included in your plan rather than switched on here.'
 WHERE value='TROUBLESHOOT';

-- AI assistant
UPDATE public.feature SET category='assistant', polarity='opt_in',
       display_name='Memory across conversations',
       description='The assistant remembers your preferences, decisions you''ve made and problems that keep coming back, and uses them in later conversations. Some of what it learns is shared with everyone in your company. Off means it uses only the current conversation.'
 WHERE value='MEMORY_MODULE';

UPDATE public.feature SET category='assistant', polarity='opt_in',
       display_name='Assistant can run automations',
       description='Lets the assistant run automations you have opted in, plus a set of built-in actions. Every run still asks you to confirm in chat.'
 WHERE value='AI_WORKFLOW_TOOLS';

UPDATE public.feature SET category='assistant', polarity='opt_in',
       display_name='Follow chat channels',
       description='Nubi reads along in the channels you opt in, so it has the conversation as context when you mention it.'
 WHERE value='CHANNEL_AWARENESS';

UPDATE public.feature SET category='assistant', polarity='opt_in',
       display_name='Custom AI functions',
       description='Shows the Functions tab, where you can create and edit your own AI functions and attach them to alerts.'
 WHERE value='LLM_FUNCTION';

-- Cost & optimisation
UPDATE public.feature SET category='cost', polarity='opt_in',
       display_name='AI usage and cost tab',
       description='Adds a tab on Optimise showing what your AI usage costs, by model and account.'
 WHERE value='LLM_ANALYSER';

UPDATE public.feature SET category='cost', polarity='opt_in',
       display_name='Daily AI cost digest',
       description='Sends a daily and month-to-date AI spend summary to Slack, and to the dashboard.'
 WHERE value='AI_COST_REPORT';

UPDATE public.feature SET category='cost', polarity='opt_in',
       display_name='Kubernetes upgrade planner',
       description='Reads your cluster''s configuration and produces a step-by-step upgrade plan.'
 WHERE value='UPGRADE_PLANNER';

UPDATE public.feature SET category='cost', polarity='opt_in',
       display_name='Optimisation',
       description='AI cost optimisation and recommendations. Included in your plan rather than switched on here.'
 WHERE value='OPTIMIZE';

-- Monitoring & detection
UPDATE public.feature SET category='monitoring', polarity='opt_in',
       display_name='Anomaly detection',
       description='Flags unusual CPU, memory, latency, replica-count and cloud-spend behaviour.'
 WHERE value='ANOMALY_DETECTION';

UPDATE public.feature SET category='monitoring', polarity='opt_in',
       display_name='Identify what a webhook alert is about',
       description='Works out which service or resource an incoming webhook alert refers to.'
 WHERE value='WEBHOOK_LLM_RESOLUTION';

-- Access & platform
UPDATE public.feature SET category='access', polarity='opt_in',
       display_name='Custom roles',
       description='Lets you create your own roles with exactly the permissions you choose, on top of the built-in ones. Off means only the built-in roles apply, and anyone relying on a custom role loses the extra access it gave them. Only a company admin can change this.'
 WHERE value='CUSTOM_ROLES';

-- Anything the backfill did not name -- rows this branch has not seen, or flags
-- registered by a migration still in flight -- gets something usable rather than
-- blocking the NOT NULL below. 'other' sorts last and reads as unclassified.
UPDATE public.feature SET display_name = initcap(replace(value, '_', ' ')) WHERE display_name IS NULL;
UPDATE public.feature SET category = 'other'  WHERE category IS NULL;
UPDATE public.feature SET polarity = 'opt_in' WHERE polarity IS NULL;

ALTER TABLE public.feature
    DROP CONSTRAINT IF EXISTS feature_category_fkey,
    DROP CONSTRAINT IF EXISTS feature_polarity_check;

ALTER TABLE public.feature
    ADD CONSTRAINT feature_category_fkey FOREIGN KEY (category)
        REFERENCES public.feature_category (id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    ADD CONSTRAINT feature_polarity_check CHECK (polarity IN ('opt_in', 'default_on')),
    ALTER COLUMN display_name SET NOT NULL,
    ALTER COLUMN category     SET NOT NULL,
    ALTER COLUMN polarity     SET NOT NULL;

-- display_name and category have no default on purpose: registering a new flag
-- should require deciding what it is called and where it belongs. polarity and
-- stored_value_inverted default to the safe, common answer.
ALTER TABLE public.feature ALTER COLUMN polarity SET DEFAULT 'opt_in';

-- Retire three flags nobody is steering with.
--
-- The Go code that reads these three is deliberately NOT removed here; the
-- deletion is a runtime no-op without it, because the readers already answer
-- the same way once the row is gone:
--
--   IsFeatureEnabledByDefault queries feature_flag, not feature, and returns
--   true when it finds no row -- so ANOMALY_DETECTION_ERROR_RATE and
--   OPENCOST_SERVER_SIDE_SPEND, both default-ON with nobody opted out, keep
--   running exactly as they do today.
--
--   IsFeatureEnabled returns false for a tenant absent from the enabled list
--   -- so RBAC_K8S, which is enabled in no environment (dev holds two rows,
--   both 'disabled'; prod zero), stays off.
--
-- Every guard clause therefore keeps taking the branch it takes today, in
-- either deploy order. Removing the now-unreachable read sites means touching
-- authz and the query permission filters, which is tracked separately.
--
-- Tenant rows go first: feature_flag_feature_fkey is ON DELETE RESTRICT, so
-- deleting the catalog row while rows reference it aborts the migration. This
-- is the failure V883 and V889 hit on prod after passing on a dev database
-- that happened to have no rows.
DELETE FROM public.feature_flag
 WHERE feature_id IN ('RBAC_K8S', 'ANOMALY_DETECTION_ERROR_RATE', 'OPENCOST_SERVER_SIDE_SPEND');

DELETE FROM public.feature
 WHERE value IN ('RBAC_K8S', 'ANOMALY_DETECTION_ERROR_RATE', 'OPENCOST_SERVER_SIDE_SPEND');
