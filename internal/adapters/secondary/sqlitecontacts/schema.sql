-- sqlitecontacts schema v1. Embedded via go:embed. Idempotent.

CREATE TABLE IF NOT EXISTS contacts (
    jid                        TEXT PRIMARY KEY,
    e164                       TEXT,
    first_name                 TEXT,
    full_name                  TEXT,
    push_name                  TEXT,
    business_name              TEXT,
    display_name               TEXT NOT NULL,
    redacted_phone             TEXT,
    profile_pic_url            TEXT,
    profile_pic_id             TEXT,
    profile_pic_sha256         BLOB,
    profile_pic_fetched_at     INTEGER,
    is_business                INTEGER NOT NULL DEFAULT 0,
    is_blocked                 INTEGER NOT NULL DEFAULT 0,
    is_contact_in_address_book INTEGER NOT NULL DEFAULT 0,
    last_seen_ts               INTEGER,
    last_interaction_ts        INTEGER,
    first_seen_at              INTEGER NOT NULL,
    updated_at                 INTEGER NOT NULL,
    updated_seq                INTEGER NOT NULL,
    notes                      TEXT NOT NULL DEFAULT '',
    tags                       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_contacts_updated_seq ON contacts (updated_seq);
CREATE INDEX IF NOT EXISTS idx_contacts_e164 ON contacts (e164);

CREATE VIRTUAL TABLE IF NOT EXISTS contacts_fts USING fts5(
    display_name, push_name, business_name, notes,
    content='contacts',
    content_rowid='rowid',
    tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS contacts_ai AFTER INSERT ON contacts BEGIN
    INSERT INTO contacts_fts(rowid, display_name, push_name, business_name, notes)
    VALUES (new.rowid, new.display_name, COALESCE(new.push_name, ''), COALESCE(new.business_name, ''), new.notes);
END;
CREATE TRIGGER IF NOT EXISTS contacts_ad AFTER DELETE ON contacts BEGIN
    INSERT INTO contacts_fts(contacts_fts, rowid, display_name, push_name, business_name, notes)
    VALUES ('delete', old.rowid, old.display_name, COALESCE(old.push_name, ''), COALESCE(old.business_name, ''), old.notes);
END;
CREATE TRIGGER IF NOT EXISTS contacts_au AFTER UPDATE ON contacts BEGIN
    INSERT INTO contacts_fts(contacts_fts, rowid, display_name, push_name, business_name, notes)
    VALUES ('delete', old.rowid, old.display_name, COALESCE(old.push_name, ''), COALESCE(old.business_name, ''), old.notes);
    INSERT INTO contacts_fts(rowid, display_name, push_name, business_name, notes)
    VALUES (new.rowid, new.display_name, COALESCE(new.push_name, ''), COALESCE(new.business_name, ''), new.notes);
END;

PRAGMA user_version = 1;
