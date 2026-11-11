package rest

import (
	"encoding/json"
	"testing"
)

// TestAuthWhoami_MatchesRequiredScope is FR-116-12: every token scope may
// call auth.whoami, which reports that scope and, for every method in the
// table, exactly what RequiredScope (the enforcement path) says.
func TestAuthWhoami_MatchesRequiredScope(t *testing.T) {
	store := newFakeStore()
	for _, s := range []Scope{ScopeReadStr, ScopeSendStr, ScopeAdminStr} {
		store.Add("tok-"+string(s), s)
	}
	addr := newScopedServerForTest(t, store).ListenerAddr().String()
	whoamiAs := func(token string, params any) whoamiResult {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "auth.whoami", "params": params})
		resp := postScoped(t, addr, token, string(body))
		defer func() { _ = resp.Body.Close() }()
		var env struct {
			Result whoamiResult `json:"result"`
			Error  *rpcError    `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.Error != nil {
			t.Fatalf("auth.whoami %v as %s: status %d err %v rpc %+v", params, token, resp.StatusCode, err, env.Error)
		}
		return env.Result
	}
	for _, s := range []Scope{ScopeReadStr, ScopeSendStr, ScopeAdminStr} {
		if got := whoamiAs("tok-"+string(s), nil); got.Scope != string(s) || got.RequiredScope != "" {
			t.Errorf("bare whoami as %s = %+v", s, got)
		}
	}
	for method := range MethodScopes {
		want, _ := RequiredScope(method, nil)
		if got := whoamiAs("tok-read", map[string]string{"method": method}); got.RequiredScope != want.String() || got.Scope != "read" {
			t.Errorf("%s: whoami %+v, RequiredScope %s", method, got, want)
		}
	}
	selfRevoke := map[string]any{"method": revokeMethod, "params": map[string]string{"scope": "self"}}
	if want, _ := RequiredScope(revokeMethod, json.RawMessage(`{"scope":"self"}`)); whoamiAs("tok-send", selfRevoke).RequiredScope != want.String() {
		t.Errorf("self-scoped revoke must report %s", want)
	}
	if got := whoamiAs("tok-read", map[string]string{"method": "no.such"}); got.RequiredScope != "" {
		t.Errorf("unknown method reported scope %q", got.RequiredScope)
	}
}
