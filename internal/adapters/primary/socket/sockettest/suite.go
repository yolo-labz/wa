package sockettest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
)

// MethodCase pairs the positive- and error-case assertions for a single
// JSON-RPC method declared in `contracts/jsonrpc-v2.json`. TestSocketContract
// CoversAllMethods walks the contract file and insists every declared method
// has a registered MethodCase whose Positive and Error funcs are non-nil.
// Tier-1 registers placeholder cases that t.Skip into Tier-2 wiring;
// Tier-2/Tier-3 replace those with real assertions by calling
// RegisterMethodCase again under the same method name (last write wins).
type MethodCase struct {
	// Positive exercises a well-formed request that returns a successful result
	// envelope.
	Positive func(t *testing.T)
	// Error exercises an invalid-param, policy-refused, or rate-limited path
	// returning a JSON-RPC error envelope with the expected code.
	Error func(t *testing.T)
}

var (
	methodCaseRegistryMu sync.Mutex
	methodCaseRegistry   = map[string]MethodCase{}
)

// RegisterMethodCase records the positive/error case pair for method `name`.
// Safe to call from init() in test files; later registrations overwrite
// earlier ones so Tier-2 wiring can supplant Tier-1 placeholders without
// touching the registry scaffold.
func RegisterMethodCase(name string, c MethodCase) {
	methodCaseRegistryMu.Lock()
	defer methodCaseRegistryMu.Unlock()
	methodCaseRegistry[name] = c
}

// snapshotRegistry returns a stable copy of the registry state at call time
// so TestSocketContractCoversAllMethods iterates without holding the mutex.
func snapshotRegistry() map[string]MethodCase {
	methodCaseRegistryMu.Lock()
	defer methodCaseRegistryMu.Unlock()
	out := make(map[string]MethodCase, len(methodCaseRegistry))
	for k, v := range methodCaseRegistry {
		out[k] = v
	}
	return out
}

// ContractMethods parses the frozen `contracts/jsonrpc-v2.json` schema and
// returns the sorted list of method names declared under
// `properties.methods.properties`. It locates the contract relative to this
// source file via runtime.Caller so the suite does not need a pre-staged
// copy. Fails the test if the contract cannot be read or decoded — that
// would mean the repo moved and the parity check is no longer anchored.
func ContractMethods(t testing.TB) []string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("sockettest: runtime.Caller(0) failed")
	}
	// thisFile: .../internal/adapters/primary/socket/sockettest/suite.go
	// repo root is five levels up from the containing directory.
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..", ".."))
	path := filepath.Join(repoRoot, "specs", "018-parity-hardening", "contracts", "jsonrpc-v2.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("sockettest: read contract %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Methods struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"methods"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("sockettest: decode contract %s: %v", path, err)
	}
	out := make([]string, 0, len(doc.Properties.Methods.Properties))
	for m := range doc.Properties.Methods.Properties {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
