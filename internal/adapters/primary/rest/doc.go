// Package rest is the secondary HTTP primary adapter that exposes
// the dispatcher over JSON-RPC 2.0 + bearer-token auth, parallel to
// the unix-socket primary adapter. Spec 110.
//
// 110a (this commit): POST /v1/rpc handler + auth middleware. Single
// env-var bearer token (WAD_REST_TOKEN) — production-ready bootstrap
// surface. Multi-token sqlite-backed admin lands in 110c.
//
// 110b (future): GET /v1/events SSE bridge.
//
// 110c (future): sqlite tokens table + wad token admin + wa --remote
// CLI mode + keyring integration on the client side.
//
// The package is internal/adapters/primary so the depguard rule
// `core-no-whatsmeow` does not apply (this is allowed to import
// app + domain but not whatsmeow). Cross-checked against
// .golangci.yml at PR time.
package rest
