-- The engine's schema (SAAS.md §6). The management app writes orgs, aliases, keys, mappings and file entries; the
-- engine reads them, and writes the audit log. Every change tells the engine instances through NOTIFY.

CREATE TABLE engine.orgs (
    id                   text PRIMARY KEY,
    handle               text NOT NULL UNIQUE,
    status               text NOT NULL DEFAULT 'active'
                         CHECK (status IN ('trialing', 'active', 'past_due', 'suspended', 'cancelled', 'deleted')),
    allowlist            cidr[] NOT NULL DEFAULT '{}',
    allowlist_admin      boolean NOT NULL DEFAULT false,
    requests_per_second  double precision NOT NULL DEFAULT 0,
    burst                integer NOT NULL DEFAULT 0,
    monthly_request_cap  bigint NOT NULL DEFAULT 0,
    grace                bigint NOT NULL DEFAULT 0,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- An older handle that still resolves to the org. Aliases are for good.
CREATE TABLE engine.org_aliases (
    alias   text PRIMARY KEY,
    org_id  text NOT NULL REFERENCES engine.orgs (id)
);

-- Tool app keys. Only the SHA-256 of a key is stored.
CREATE TABLE engine.api_keys (
    hash          bytea PRIMARY KEY CHECK (octet_length(hash) = 32),
    org_id        text NOT NULL REFERENCES engine.orgs (id),
    label         text NOT NULL,
    scopes        text[] NOT NULL,
    expires_at    timestamptz,
    revoked_at    timestamptz,
    last_used_at  timestamptz,
    last_used_ip  text
);
CREATE INDEX api_keys_org_idx ON engine.api_keys (org_id);

-- The global layer of mappings, written by the management app.
CREATE TABLE engine.mappings (
    org_id    text NOT NULL REFERENCES engine.orgs (id),
    id        text NOT NULL,
    position  bigserial,
    body      jsonb NOT NULL,
    PRIMARY KEY (org_id, id)
);
CREATE INDEX mappings_order_idx ON engine.mappings (org_id, position);

-- The names of an org's files; the bytes are in object storage under files/<org>/<sha256> (FR-11.13).
CREATE TABLE engine.files (
    org_id        text NOT NULL REFERENCES engine.orgs (id),
    name          text NOT NULL,
    sha256        bytea NOT NULL CHECK (octet_length(sha256) = 32),
    size          bigint NOT NULL,
    content_type  text NOT NULL DEFAULT '',
    PRIMARY KEY (org_id, name)
);

CREATE TABLE engine.audit_log (
    id         bigserial PRIMARY KEY,
    at         timestamptz NOT NULL,
    org_id     text NOT NULL,
    issuer     text NOT NULL DEFAULT '',
    subject    text NOT NULL,
    key_label  text NOT NULL DEFAULT '',
    token_id   text NOT NULL DEFAULT '',
    method     text NOT NULL,
    route      text NOT NULL,
    status     integer NOT NULL
);
CREATE INDEX audit_log_org_idx ON engine.audit_log (org_id, id DESC);

-- notify_change tells the engine instances that something changed. The payload is a hint, not the change: the
-- listener reloads what it names, and a poll catches any notice that was missed.
CREATE FUNCTION engine.notify_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    row_json jsonb;
BEGIN
    IF TG_OP = 'DELETE' THEN
        row_json := to_jsonb(OLD);
    ELSE
        row_json := to_jsonb(NEW);
    END IF;
    PERFORM pg_notify('sm_changes', json_build_object(
        'kind', TG_ARGV[0],
        'tenant', coalesce(row_json ->> 'org_id', row_json ->> 'id')
    )::text);
    RETURN NULL;
END
$$;

CREATE TRIGGER orgs_notify AFTER INSERT OR UPDATE OR DELETE ON engine.orgs
    FOR EACH ROW EXECUTE FUNCTION engine.notify_change('org');
CREATE TRIGGER org_aliases_notify AFTER INSERT OR UPDATE OR DELETE ON engine.org_aliases
    FOR EACH ROW EXECUTE FUNCTION engine.notify_change('org');
CREATE TRIGGER api_keys_notify AFTER INSERT OR UPDATE OF revoked_at, expires_at, scopes OR DELETE ON engine.api_keys
    FOR EACH ROW EXECUTE FUNCTION engine.notify_change('token');
CREATE TRIGGER mappings_notify AFTER INSERT OR UPDATE OR DELETE ON engine.mappings
    FOR EACH ROW EXECUTE FUNCTION engine.notify_change('mapping');
CREATE TRIGGER files_notify AFTER INSERT OR UPDATE OR DELETE ON engine.files
    FOR EACH ROW EXECUTE FUNCTION engine.notify_change('file');
