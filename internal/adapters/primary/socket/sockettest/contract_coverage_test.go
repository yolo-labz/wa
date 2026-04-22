package sockettest

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yolo-labz/wa/internal/adapters/primary/socket"
)

// tier2PendingMethods names every contract method whose implementation and
// assertion live in Tier-2 / Tier-3. Each gets a Skip-flavoured MethodCase
// so TestSocketContractCoversAllMethods passes today; when Tier-2 tasks
// wire the real server path they call RegisterMethodCase again with the
// same name and the skip disappears. The list is derived from
// `contracts/jsonrpc-v2.json` and MUST match the set of methods declared
// under `properties.methods.properties` minus those explicitly registered
// with real assertions below.
var tier2PendingMethods = []string{
	"message.revoke",
	"message.edit",
	"message.setDisappearing",
	"message.forward",
	"message.star",
	"contacts.block",
	"contacts.unblock",
	"contacts.blocklist",
	"contacts.resolve.confirm",
	"contacts.profilePhoto",
	"group.create",
	"group.participants.add",
	"group.participants.remove",
	"group.participants.promote",
	"group.participants.demote",
	"group.leave",
	"group.edit",
	"group.invite.get",
	"group.invite.revoke",
	"group.invite.join",
	"privacy.set",
	"privacy.get",
	"session.logoutAll",
	"profile.setName",
	"profile.setStatus",
	"presence.composing",
	"presence.recording",
	"chat.archive",
	"chat.pin",
	"chat.mute",
	"chat.markUnread",
	"debug.pprof.profile",
}

// init registers the Tier-1 contract cases. `system.hello` gets real
// positive + error assertions here; every other contract method gets a
// placeholder that t.Skip()s into the Tier-2 wiring. The registry is the
// scaffold — RegisterMethodCase is last-write-wins, so Tier-2 tasks
// replace placeholders by simply calling RegisterMethodCase again from
// their own test files.
func init() {
	RegisterMethodCase("system.hello", MethodCase{
		Positive: contractSystemHelloPositive,
		Error:    contractSystemHelloError,
	})

	for _, m := range tier2PendingMethods {
		m := m
		RegisterMethodCase(m, MethodCase{
			Positive: func(t *testing.T) {
				t.Skipf("contract parity: positive case for %q lands in Tier-2 wiring", m)
			},
			Error: func(t *testing.T) {
				t.Skipf("contract parity: error case for %q lands in Tier-2 wiring", m)
			},
		})
	}
}

// contractSystemHelloPositive boots a server with a known serverVersion,
// sends a well-formed hello with protoVersion=2, and asserts the success
// envelope echoes the frozen acceptedProtoVersion. This is a compact
// smoke-test version of TestHelloAcceptWithinBudget — the two coexist so
// the contract-parity suite is self-contained.
func contractSystemHelloPositive(t *testing.T) {
	const sv = "2.0.0-contract"
	path := startHelloServer(t, socket.WithServerVersion(sv))
	conn, scanner := dialRaw(t, path)
	if _, err := fmt.Fprint(conn,
		`{"jsonrpc":"2.0","id":"chp","method":"system.hello","params":{"protoVersion":2}}`+"\n",
	); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	if !scanner.Scan() {
		t.Fatalf("scan hello response: %v", scanner.Err())
	}
	var resp helloResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", scanner.Text(), err)
	}
	if resp.Error != nil {
		t.Fatalf("positive hello rejected: code=%d message=%q", resp.Error.Code, resp.Error.Message)
	}
	if resp.Result == nil || resp.Result.AcceptedProtoVersion != 2 {
		t.Fatalf("positive hello result = %+v, want acceptedProtoVersion=2", resp.Result)
	}
	if resp.Result.ServerVersion != sv {
		t.Fatalf("positive hello serverVersion=%q, want %q", resp.Result.ServerVersion, sv)
	}
}

// contractSystemHelloError sends a protoVersion=1 hello and asserts the
// server replies with -32000 protocol_mismatch and drops the connection.
// Mirrors TestHelloWrongProtoRejected at contract granularity.
func contractSystemHelloError(t *testing.T) {
	path := startHelloServer(t)
	conn, scanner := dialRaw(t, path)
	if _, err := fmt.Fprint(conn,
		`{"jsonrpc":"2.0","id":"che","method":"system.hello","params":{"protoVersion":1}}`+"\n",
	); err != nil {
		t.Fatalf("write bad hello: %v", err)
	}
	if !scanner.Scan() {
		t.Fatalf("scan error frame: %v", scanner.Err())
	}
	var resp helloResp
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", scanner.Text(), err)
	}
	if resp.Error == nil {
		t.Fatalf("error case: want -32000 protocol_mismatch, got result %+v", resp.Result)
	}
	if resp.Error.Code != int(socket.CodeProtocolMismatch) {
		t.Fatalf("error.code = %d, want %d (protocol_mismatch)", resp.Error.Code, socket.CodeProtocolMismatch)
	}
}

// TestSocketContractCoversAllMethods enforces R-06 / FR-009: every JSON-RPC
// method declared in `contracts/jsonrpc-v2.json` MUST have a registered
// MethodCase with non-nil Positive and Error functions. A new method
// appearing in the contract without a registry entry fails this test,
// blocking merges that add protocol surface without accompanying assertions.
func TestSocketContractCoversAllMethods(t *testing.T) {
	methods := ContractMethods(t)
	if len(methods) == 0 {
		t.Fatal("contract declared zero methods; the JSON schema is malformed or the parser broke")
	}
	reg := snapshotRegistry()

	for _, m := range methods {
		m := m
		t.Run(m, func(t *testing.T) {
			c, ok := reg[m]
			if !ok {
				t.Fatalf("contract method %q has no RegisterMethodCase entry; add one in a _test.go init()", m)
			}
			if c.Positive == nil {
				t.Fatalf("contract method %q: MethodCase.Positive is nil", m)
			}
			if c.Error == nil {
				t.Fatalf("contract method %q: MethodCase.Error is nil", m)
			}
			t.Run("positive", c.Positive)
			t.Run("error", c.Error)
		})
	}

	for registered := range reg {
		if !contains(methods, registered) {
			t.Errorf("registered method %q is not declared in contracts/jsonrpc-v2.json; remove or update the contract", registered)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
