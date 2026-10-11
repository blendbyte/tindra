-- A rule with no rows in alert_rule_projects used to mean "all projects".
-- Deleting a rule's last project (ON DELETE CASCADE) therefore silently made a
-- project-scoped rule global. Store the intent explicitly instead.
ALTER TABLE alert_rules ADD COLUMN is_global BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE alert_rules r SET is_global = TRUE
WHERE NOT EXISTS (SELECT 1 FROM alert_rule_projects p WHERE p.rule_id = r.id);
