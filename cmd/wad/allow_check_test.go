package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"

	"github.com/yolo-labz/wa/v2/internal/adapters/primary/rest"
	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

const (
	readOnlyNum = "5511900000001" // allowlisted for read only
	senderNum   = "5511900000002" // allowlisted for send
	strangerNum = "5511900000003" // not allowlisted at all
)

func allowCheckFixture() (*domain.Allowlist, *recordingAudit, compositionHandler) {
	al := domain.NewAllowlist()
	al.Grant(domain.MustJID(readOnlyNum), domain.ActionRead)
	al.Grant(domain.MustJID(senderNum), domain.ActionSend)
	audit := &recordingAudit{}
	return al, audit, handleAllowCheck(al, &sync.RWMutex{}, audit, func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) })
}

func check(t *testing.T, h compositionHandler, params string) (json.RawMessage, error) {
	t.Helper()
	return h(t.Context(), json.RawMessage(params))
}

// TestAllowCheck_ReadScopeAllowed: a read token may ask, end to end over
// /mcp, while listing the allowlist stays admin.
func TestAllowCheck_ReadScopeAllowed(t *testing.T) {
	if s, ok := rest.RequiredScope("allow.check", nil); !ok || s != rest.ScopeRead {
		t.Fatalf("allow.check requires %v (known=%v), want read", s, ok)
	}
	if rest.AllowedScope("allow", json.RawMessage(`{"op":"list"}`), rest.ScopeSend) {
		t.Fatal("allow op=list must stay admin")
	}
	_, _, h := allowCheckFixture()
	d := handlerDispatcher{func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
		if method != "allow.check" {
			return nil, errors.New("unexpected " + method)
		}
		return h(ctx, params)
	}}
	cs := mcpHTTPSession(t, d, rest.ScopeReadStr)
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "wa_allowlist_check", Arguments: map[string]any{"recipient": senderNum, "action": "send"},
	})
	if err != nil || res.IsError {
		t.Fatalf("read-scope pre-flight: %v %+v", err, res)
	}
	got, _ := json.Marshal(res.StructuredContent)
	if string(got) != `{"action":"send","allowed":true}` {
		t.Fatalf("result %s", got)
	}
}

// TestAllowCheck_DoesNotEnumerate: the answer covers one (recipient,
// action) pair; a recipient allowlisted for other actions reads exactly
// like a stranger, and no other action or recipient leaks.
func TestAllowCheck_DoesNotEnumerate(t *testing.T) {
	_, _, h := allowCheckFixture()
	readOnly, err := check(t, h, `{"jid":"`+readOnlyNum+`","action":"send"}`)
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := check(t, h, `{"jid":"`+strangerNum+`","action":"send","op":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(readOnly) != string(stranger) {
		t.Errorf("read-only recipient %s differs from stranger %s", readOnly, stranger)
	}
	var keys map[string]any
	_ = json.Unmarshal(stranger, &keys)
	if want := []string{"action", "allowed", "remediation"}; !reflect.DeepEqual(sortedKeys(keys), want) {
		t.Errorf("denial fields %v, want %v", sortedKeys(keys), want)
	}
	if strings.Contains(string(stranger), "55119") || strings.Contains(string(stranger), "read") {
		t.Errorf("denial leaks a recipient or another action: %s", stranger)
	}
	sender, err := check(t, h, `{"jid":"`+senderNum+`","action":"send"}`)
	if err != nil || string(sender) != `{"allowed":true,"action":"send"}` {
		t.Errorf("allowed answer %s (%v)", sender, err)
	}
	for _, bad := range []string{`{"jid":"` + senderNum + `","action":"list"}`, `{"jid":"` + senderNum + `"}`, `{"jid":"","action":"send"}`} {
		if _, err := check(t, h, bad); !errors.Is(err, app.ErrInvalidParams) {
			t.Errorf("%s: err %v, want invalid params", bad, err)
		}
	}
}

// TestAllowCheck_Audited: every decision is one audit record, the check
// never mutates the allowlist, an audit failure fails it closed, and the
// method is rate-limited against probing.
func TestAllowCheck_Audited(t *testing.T) {
	al, audit, h := allowCheckFixture()
	before := al.Entries()
	for _, num := range []string{senderNum, strangerNum} {
		if _, err := check(t, h, `{"jid":"`+num+`","action":"send"}`); err != nil {
			t.Fatal(err)
		}
	}
	events := audit.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit records %d, want 2", len(events))
	}
	for i, want := range []struct{ subject, decision string }{{senderNum, "allowed"}, {strangerNum, "denied"}} {
		e := events[i]
		if e.Action != domain.AuditAllowCheck || e.Subject != domain.MustJID(want.subject) || e.Decision != want.decision || e.Detail != "action=send" {
			t.Errorf("event %d = %+v, want allow_check %s %s", i, e, want.subject, want.decision)
		}
	}
	if !reflect.DeepEqual(before, al.Entries()) {
		t.Error("allow.check mutated the allowlist")
	}
	failing := handleAllowCheck(al, &sync.RWMutex{}, failAudit{}, func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 1) })
	if res, err := check(t, failing, `{"jid":"`+senderNum+`","action":"send"}`); err == nil || res != nil {
		t.Errorf("audit failure answered %s (%v), want fail-closed", res, err)
	}
	limited := handleAllowCheck(al, &sync.RWMutex{}, &recordingAudit{}, func() *rate.Limiter { return rate.NewLimiter(0, 1) })
	_, _ = check(t, limited, `{"jid":"`+senderNum+`","action":"send"}`)
	if _, err := check(t, limited, `{"jid":"`+senderNum+`","action":"send"}`); !errors.Is(err, app.ErrRateLimited) {
		t.Errorf("second check over budget: err %v, want rate limited", err)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// idTokens is a TokenStore whose raw token is its own id, so a test can
// put any principal on a request context through the real scopedAuth.
type idTokens struct{}

func (idTokens) Verify(_ context.Context, raw string) (rest.Scope, string, error) {
	return rest.ScopeReadStr, raw, nil
}

func principalCtx(t *testing.T, id string) context.Context {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/rpc", nil)
	req.Header.Set("Authorization", "Bearer "+id)
	if err := rest.NewScopedAuth(idTokens{}).Verify(req); err != nil {
		t.Fatal(err)
	}
	return req.Context()
}

// TestAllowCheck_PerPrincipalLimit pins WA-MCP-SEC F6: each verified token
// has its own allow.check budget, so one token exhausting its budget
// cannot starve another (or the local socket), and a token cannot dodge
// its budget except by holding more tokens.
func TestAllowCheck_PerPrincipalLimit(t *testing.T) {
	t.Parallel()
	h := handleAllowCheck(domain.NewAllowlist(), &sync.RWMutex{}, &recordingAudit{}, func() *rate.Limiter { return rate.NewLimiter(0, 2) })
	params := json.RawMessage(`{"jid":"` + senderNum + `","action":"send"}`)
	a, b := principalCtx(t, "tok-a"), principalCtx(t, "tok-b")
	for i := range 2 {
		if _, err := h(a, params); err != nil {
			t.Fatalf("tok-a check %d within budget: %v", i, err)
		}
	}
	if _, err := h(a, params); !errors.Is(err, app.ErrRateLimited) {
		t.Fatalf("tok-a over budget: err %v, want rate limited", err)
	}
	if _, err := h(b, params); err != nil {
		t.Fatalf("tok-b starved by tok-a: %v", err)
	}
	if _, err := h(t.Context(), params); err != nil {
		t.Fatalf("local caller starved by tok-a: %v", err)
	}
}
