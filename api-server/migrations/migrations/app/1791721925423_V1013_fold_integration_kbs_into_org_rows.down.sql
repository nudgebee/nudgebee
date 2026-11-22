-- Intentional no-op. The up migration deletes the per-account integration rows
-- after moving their marks, images and switch-offs to one organisation row per
-- integration; which accounts had a row (the integration's linked accounts at the
-- time) is not recorded anywhere, so the rows cannot be rebuilt. The fold is
-- one-way (#40605): rolling llm-server back past this release is not supported
-- for integration knowledge bases.
SELECT 1;
