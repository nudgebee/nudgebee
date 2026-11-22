-- Restores the catalog entry as V831/V900 left it (opt-in). Per-tenant flag
-- rows are not restored, and the code that read them must be rolled back too.
INSERT INTO "public"."feature" ("value", "description", "display_name", "category", "polarity", "stored_value_inverted")
VALUES ('CUSTOM_ROLES',
        'Lets you create your own roles with exactly the permissions you choose, on top of the built-in ones. Off means only the built-in roles apply, and anyone relying on a custom role loses the extra access it gave them. Only a company admin can change this.',
        'Custom roles', 'access', 'opt_in', false)
ON CONFLICT ("value") DO NOTHING;
