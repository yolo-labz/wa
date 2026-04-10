# Contract: Allowlist TOML Schema

**Feature**: 006-binaries-wiring

## File location

`$XDG_CONFIG_HOME/wa/allowlist.toml` (Linux: `~/.config/wa/allowlist.toml`, macOS: same via adrg/xdg).

## Schema

```toml
# Allowlist — controls which JIDs the daemon is permitted to send to.
# Default deny: if a JID is not listed here, all outbound actions are refused.
# Edited via `wa allow add/remove` or by hand (daemon hot-reloads on change).

[[rules]]
jid = "5511999999999@s.whatsapp.net"
actions = ["send", "read"]

[[rules]]
jid = "120363012345678@g.us"
actions = ["send"]
```

### Fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `rules` | array of tables | yes | Empty array = default deny (no sends allowed) |
| `rules[].jid` | string | yes | Must be a valid `user@server` JID per domain.ParseJID |
| `rules[].actions` | array of strings | yes | Valid values: `"send"`, `"read"`, `"group.add"`, `"group.create"` |

### Persistence rules

1. **Write**: atomic write-then-rename. Write to `allowlist.toml.tmp`, then `os.Rename` to `allowlist.toml`. Prevents partial reads.
2. **Read on startup**: if the file doesn't exist, start with empty allowlist.
3. **Reload on change**: fsnotify on parent dir (D3) + SIGHUP fallback. Debounce 100ms.
4. **Malformed file**: log ERROR, keep previous valid in-memory state.
5. **Unknown fields**: ignored (forward compatibility). Log WARN naming the field.
6. **File permissions**: `0600` on the file, `0700` on the parent directory.

### Audit

- `allow add` → `AuditGrant` entry
- `allow remove` → `AuditRevoke` entry
- `allow list` → no audit entry
