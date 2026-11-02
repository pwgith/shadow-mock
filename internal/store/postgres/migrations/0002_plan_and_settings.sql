-- What an org's plan allows beyond the request rate, and the choices an org makes within it (REQUIREMENTS.md
-- FR-11.11d, NFR-4.2). The management app writes the columns; the engine reads them. A zero limit means the server's
-- own setting, and the defaults of the settings are the engine's: everything journalled, with bodies, compressed.
ALTER TABLE engine.orgs
    ADD COLUMN max_sessions            integer NOT NULL DEFAULT 0 CHECK (max_sessions >= 0),
    ADD COLUMN included_sessions       integer NOT NULL DEFAULT 0 CHECK (included_sessions >= 0),
    ADD COLUMN max_mappings            integer NOT NULL DEFAULT 0 CHECK (max_mappings >= 0),
    ADD COLUMN max_mappings_per_session integer NOT NULL DEFAULT 0 CHECK (max_mappings_per_session >= 0),
    ADD COLUMN max_url_ttl_seconds     integer NOT NULL DEFAULT 0 CHECK (max_url_ttl_seconds >= 0),
    -- Features the plan lacks. A feature that is off keeps its settings: the allowlist stays stored and does not apply.
    ADD COLUMN excluded_features       text[] NOT NULL DEFAULT '{}',
    ADD COLUMN journal_capture         text NOT NULL DEFAULT 'all'
                                       CHECK (journal_capture IN ('all', 'sessions-and-unmatched', 'off')),
    ADD COLUMN journal_bodies          boolean NOT NULL DEFAULT true,
    ADD COLUMN compression             boolean NOT NULL DEFAULT true;
