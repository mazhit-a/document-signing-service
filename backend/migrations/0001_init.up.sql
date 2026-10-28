CREATE TYPE document_status AS ENUM ('draft', 'in_progress', 'completed', 'rejected', 'cancelled');
CREATE TYPE signer_status AS ENUM ('waiting', 'pending', 'signed', 'rejected');

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    full_name     TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE documents (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     UUID NOT NULL REFERENCES users (id),
    title        TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    file_name    TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),
    storage_key  TEXT NOT NULL,
    sha256       TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    status       document_status NOT NULL DEFAULT 'draft',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX idx_documents_owner_created ON documents (owner_id, created_at DESC, id DESC);
CREATE INDEX idx_documents_owner_status ON documents (owner_id, status);

CREATE TABLE signers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id   UUID NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    order_index   INT NOT NULL CHECK (order_index >= 1),
    full_name     TEXT NOT NULL,
    email         TEXT NOT NULL,
    status        signer_status NOT NULL DEFAULT 'waiting',
    token_hash    TEXT UNIQUE,
    decided_at    TIMESTAMPTZ,
    reject_reason TEXT,
    UNIQUE (document_id, order_index)
);
CREATE INDEX idx_signers_document ON signers (document_id, order_index);

CREATE TABLE signatures (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    signer_id       UUID NOT NULL UNIQUE REFERENCES signers (id),
    document_id     UUID NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    document_sha256 TEXT NOT NULL,
    payload         TEXT NOT NULL,
    seal            TEXT NOT NULL,
    signed_at       TIMESTAMPTZ NOT NULL,
    ip              INET,
    user_agent      TEXT
);

CREATE TABLE audit_events (
    id          BIGSERIAL PRIMARY KEY,
    document_id UUID NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
    type        TEXT NOT NULL,
    actor       TEXT NOT NULL,
    data        JSONB NOT NULL DEFAULT '{}',
    prev_hash   TEXT NOT NULL,
    hash        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_audit_document ON audit_events (document_id, id);

CREATE FUNCTION audit_events_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only' USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();

CREATE FUNCTION documents_file_immutable() RETURNS trigger AS $$
BEGIN
    IF NEW.sha256 IS DISTINCT FROM OLD.sha256
        OR NEW.storage_key IS DISTINCT FROM OLD.storage_key
        OR NEW.file_name IS DISTINCT FROM OLD.file_name
        OR NEW.content_type IS DISTINCT FROM OLD.content_type
        OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes THEN
        RAISE EXCEPTION 'document file columns are immutable' USING ERRCODE = 'restrict_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER documents_file_immutable
    BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION documents_file_immutable();
