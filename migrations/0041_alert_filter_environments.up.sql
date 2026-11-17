-- An alert rule can match several environments. An empty list matches all of
-- them, as a NULL filter_environment did. Rules that filtered on an empty
-- string matched nothing; they now match every environment, which is what
-- leaving the field blank in the UI was meant to do.
ALTER TABLE alert_rules ADD COLUMN filter_environments TEXT[] NOT NULL DEFAULT '{}';

UPDATE alert_rules
SET filter_environments = ARRAY[filter_environment]
WHERE filter_environment IS NOT NULL AND filter_environment <> '';

ALTER TABLE alert_rules DROP COLUMN filter_environment;
