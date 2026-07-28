package main

import (
	"encoding/json"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/agentdocs"
)

// TestCatalogCodesAreClassified is the CLI-side drift guard for the
// agent-readable error catalog. internal/agentdocs/errors.json is the
// published list of every code the daemon can put on the wire; this file
// maps those codes to sysexits buckets and remediation hints. The two are
// edited independently, so a code shipped daemon-side reached the catalog,
// the REST surface and the docs while `wa` silently degraded it to exit 1
// with no hint — which is how -32017 not_on_whatsapp, the pre-send
// deliverability gate whose whole purpose is to stop an agent believing an
// undeliverable send landed, arrived at the CLI as a bare generic failure.
//
// Every catalogued code must therefore be classified: mapped to a non-generic
// bucket by rpcCodeToExit, or listed in deliberatelyGeneric with the reason.
// A new code is a compile-and-test failure until someone decides which.
func TestCatalogCodesAreClassified(t *testing.T) {
	t.Parallel()

	var catalog struct {
		Errors []struct {
			Code int    `json:"code"`
			Name string `json:"name"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(agentdocs.ErrorsJSON, &catalog); err != nil {
		t.Fatalf("parse errors.json: %v", err)
	}
	if len(catalog.Errors) == 0 {
		t.Fatal("errors.json parsed to zero entries — the guard would pass vacuously")
	}

	for _, e := range catalog.Errors {
		if rpcCodeToExit(e.Code) != exitGeneric {
			continue
		}
		if _, ok := deliberatelyGeneric[e.Code]; !ok {
			t.Errorf("errors.json documents %d (%s) but the CLI neither maps it to an exit "+
				"code nor records why it stays generic — add a rpcCodeToExit case or a "+
				"deliberatelyGeneric entry", e.Code, e.Name)
		}
	}
}

// TestDeliberatelyGenericIsHonest keeps the escape hatch from rotting: an
// entry there must name a code the catalog actually publishes, and must not
// contradict rpcCodeToExit by claiming a code is generic when it is mapped.
func TestDeliberatelyGenericIsHonest(t *testing.T) {
	t.Parallel()

	var catalog struct {
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(agentdocs.ErrorsJSON, &catalog); err != nil {
		t.Fatalf("parse errors.json: %v", err)
	}
	documented := make(map[int]bool, len(catalog.Errors))
	for _, e := range catalog.Errors {
		documented[e.Code] = true
	}

	for code, reason := range deliberatelyGeneric {
		if !documented[code] {
			t.Errorf("deliberatelyGeneric lists %d, which errors.json does not document", code)
		}
		if got := rpcCodeToExit(code); got != exitGeneric {
			t.Errorf("deliberatelyGeneric says %d stays generic (%q) but rpcCodeToExit returns %d",
				code, reason, got)
		}
		if reason == "" {
			t.Errorf("deliberatelyGeneric entry %d has an empty reason", code)
		}
	}
}
