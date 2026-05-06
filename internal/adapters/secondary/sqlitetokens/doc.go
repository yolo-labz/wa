// Package sqlitetokens is the secondary adapter that owns the local
// REST adapter token database (`tokens.db`). Spec 110d.
//
// One sidecar SQLite file per profile, keyed on a ULID token_id with
// the raw token's sha256 stored as the verification material. Tokens
// are NEVER stored in plaintext; the operator sees the raw secret
// exactly once at issue time, captured by `wad token issue`.
//
// The adapter implements `rest.Authenticator` so the REST primary
// adapter (spec 110a/110b) verifies inbound bearer tokens against
// the store transparently — no daemon-side env var required.
package sqlitetokens
