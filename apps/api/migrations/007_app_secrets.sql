-- +goose Up
-- App-level secrets (integration API keys, etc.), encrypted at rest with
-- AES-256-GCM. value_encrypted holds base64(nonce|ciphertext); the plaintext
-- never touches disk. hint is the last few characters, kept in the clear so the
-- UI can show a masked "••••1234" without decrypting. key is a stable
-- identifier like 'curseforge_api_key'.
CREATE TABLE app_secrets (
    key             TEXT PRIMARY KEY,
    value_encrypted TEXT NOT NULL,
    hint            TEXT NOT NULL DEFAULT '',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by      TEXT
);

-- +goose Down
DROP TABLE IF EXISTS app_secrets;
