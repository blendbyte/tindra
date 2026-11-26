-- Per-user project access and teams. A user either sees every project ('all',
-- the default, so nothing changes for anyone who exists when this runs) or only
-- the projects of the teams they belong to ('teams').
ALTER TABLE users ADD COLUMN project_access TEXT NOT NULL DEFAULT 'all' CHECK (project_access IN ('all', 'teams'));

-- manage_users and manage_projects administer the whole instance, so they only make
-- sense for a user who sees every project. Enforcing it here means a stored
-- permission can never silently do nothing.
ALTER TABLE users ADD CONSTRAINT users_restricted_without_admin
    CHECK (project_access = 'all' OR (NOT perm_manage_users AND NOT perm_manage_projects));

CREATE TABLE teams (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE team_members (
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user ON team_members (user_id);

-- A project can belong to several teams, and a team to several projects. A project
-- without a team is only visible to users who see every project.
CREATE TABLE project_teams (
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    team_id    UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, team_id)
);
CREATE INDEX project_teams_team ON project_teams (team_id);

-- An invitation carries the access the person gets and the teams they join when
-- they accept it. Deleting a team just drops it from the invitation.
ALTER TABLE user_invites ADD COLUMN project_access TEXT NOT NULL DEFAULT 'all' CHECK (project_access IN ('all', 'teams'));

CREATE TABLE invite_teams (
    invite_id UUID NOT NULL REFERENCES user_invites(id) ON DELETE CASCADE,
    team_id   UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    PRIMARY KEY (invite_id, team_id)
);
CREATE INDEX invite_teams_team ON invite_teams (team_id);
