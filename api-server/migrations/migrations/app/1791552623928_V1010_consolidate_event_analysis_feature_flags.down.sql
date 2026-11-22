-- Restores the two catalog rows as V900 left them. Tenant feature_flag rows are
-- not restored, and automatic-analysis rows written by the up migration stay.
INSERT INTO public.feature (value, description, display_name, category, polarity, stored_value_inverted)
VALUES
    ('GENERATE_RCA',
     'Adds a root-cause panel to each event, explaining why it happened rather than only what fired.',
     'Root cause analysis for events', 'events', 'opt_in', false),
    ('EVENT_DEBUG_ANALYSIS_DISABLED',
     'Runs a deeper diagnostic pass on each event, on top of the standard analysis.',
     'Deep event analysis', 'events', 'default_on', true)
ON CONFLICT (value) DO NOTHING;
