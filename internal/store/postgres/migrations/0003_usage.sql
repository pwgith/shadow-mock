-- What each org used, by the hour (REQUIREMENTS.md FR-11.12), kept beyond the instance that counted it. Every
-- instance adds what it counted since its last write, so restarts, deploys and several instances add up to one
-- total. Peaks (concurrent shadow mocks, stored files) keep the highest any instance saw. The management app reads
-- it to show and report usage; the monthly cap reads it after a restart.
CREATE TABLE engine.usage (
    org_id                   text NOT NULL REFERENCES engine.orgs (id),
    period_start             timestamptz NOT NULL,
    requests                 bigint NOT NULL DEFAULT 0 CHECK (requests >= 0),
    concurrent_sessions_max  integer NOT NULL DEFAULT 0 CHECK (concurrent_sessions_max >= 0),
    stored_files             integer NOT NULL DEFAULT 0 CHECK (stored_files >= 0),
    stored_bytes             bigint NOT NULL DEFAULT 0 CHECK (stored_bytes >= 0),
    rate_limited             bigint NOT NULL DEFAULT 0 CHECK (rate_limited >= 0),
    ip_rejected              bigint NOT NULL DEFAULT 0 CHECK (ip_rejected >= 0),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, period_start)
);
