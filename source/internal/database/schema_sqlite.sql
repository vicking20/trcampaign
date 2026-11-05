CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NULL COLLATE NOCASE UNIQUE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS personas (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    blaze_id INTEGER NOT NULL UNIQUE,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at TEXT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    session_key TEXT NOT NULL UNIQUE,
    session_kind TEXT NOT NULL DEFAULT 'legacy',
    credential_key TEXT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_sessions_persona ON sessions(persona_id);
CREATE INDEX IF NOT EXISTS ix_sessions_credential ON sessions(credential_key);
CREATE TABLE IF NOT EXISTS launcher_credentials (
    public_key TEXT NOT NULL PRIMARY KEY,
    persona_id INTEGER NOT NULL UNIQUE REFERENCES personas(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS launcher_claims (
    claim_hash TEXT NOT NULL PRIMARY KEY,
    persona_name TEXT NOT NULL COLLATE NOCASE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_launcher_claim_persona ON launcher_claims(persona_name);
CREATE INDEX IF NOT EXISTS ix_launcher_claim_expiry ON launcher_claims(expires_at);
CREATE TABLE IF NOT EXISTS identity_aliases (
    alias_key TEXT NOT NULL PRIMARY KEY COLLATE NOCASE,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS ix_identity_alias_persona ON identity_aliases(persona_id);
CREATE TABLE IF NOT EXISTS friends (
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    friend_persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'accepted',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (persona_id, friend_persona_id)
);
CREATE TABLE IF NOT EXISTS stats (
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    stat_key TEXT NOT NULL,
    stat_value INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (persona_id, category, stat_key)
);
CREATE TABLE IF NOT EXISTS leaderboards (
    board_key TEXT NOT NULL,
    event_id INTEGER NOT NULL DEFAULT 0,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    score INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (board_key, event_id, persona_id)
);
CREATE TABLE IF NOT EXISTS autolog_stories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    story_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    viewed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS ix_autolog_stories_persona ON autolog_stories(persona_id);
CREATE TABLE IF NOT EXISTS game_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    blaze_game_id INTEGER NULL UNIQUE,
    host_persona_id INTEGER NULL REFERENCES personas(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'created',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS server_state (
    state_key TEXT NOT NULL PRIMARY KEY,
    state_value TEXT NULL,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS game_reports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    persona_id INTEGER NULL REFERENCES personas(id) ON DELETE SET NULL,
    game_type TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL UNIQUE,
    payload_hex TEXT NOT NULL,
    results_json TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS ix_game_reports_persona ON game_reports(persona_id);
CREATE TABLE IF NOT EXISTS locker_contents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    category TEXT NOT NULL,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    attributes_json TEXT NOT NULL,
    sub_names TEXT NOT NULL DEFAULT '',
    permission INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 1,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    upload_token TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    use_count INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS ix_locker_owner ON locker_contents(persona_id, category);
CREATE INDEX IF NOT EXISTS ix_locker_category ON locker_contents(category, status);
CREATE TABLE IF NOT EXISTS locker_ratings (
    content_id INTEGER NOT NULL REFERENCES locker_contents(id) ON DELETE CASCADE,
    persona_id INTEGER NOT NULL REFERENCES personas(id) ON DELETE CASCADE,
    rating INTEGER NOT NULL,
    rated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (content_id, persona_id)
);
CREATE INDEX IF NOT EXISTS ix_locker_ratings_persona ON locker_ratings(persona_id);
CREATE TABLE IF NOT EXISTS password_logins (
    persona_id INTEGER NOT NULL PRIMARY KEY REFERENCES personas(id) ON DELETE CASCADE,
    pw_salt TEXT NOT NULL,
    pw_hash TEXT NOT NULL,
    iterations INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
