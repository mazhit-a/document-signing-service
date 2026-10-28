CREATE TABLE email_outbox (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id        UUID NOT NULL REFERENCES documents (id),
    kind               TEXT NOT NULL CHECK (kind IN ('signer_turn', 'owner_completed', 'owner_rejected')),
    recipient          TEXT NOT NULL,
    payload_ciphertext BYTEA,
    status             TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts           INT NOT NULL DEFAULT 0,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at            TIMESTAMPTZ
);
CREATE INDEX idx_email_outbox_due ON email_outbox (next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_email_outbox_document ON email_outbox (document_id);
