-- Supports paged direct-child lookup and has_children without scanning the team.
CREATE INDEX agent_relations_parent_user_idx ON agent_relations(parent_user_id, user_id);
