package domain

// ProtoVersion is the frozen JSON-RPC wire protocol version exposed by
// this build. Clients MUST send `system.hello` with protoVersion: 2 as
// the first frame; mismatches yield -32000 protocol_mismatch. No v1
// compatibility path exists per FR-012 / Removal inventory R-12.
const ProtoVersion = 2
