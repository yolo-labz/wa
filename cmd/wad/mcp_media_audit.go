package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// auditMCPMedia wraps a media read method so that every call the MCP
// bridge makes (params.origin == "mcp") leaves an audit record with
// source "mcp" (FR-116-15). An audit failure withholds the result.
func auditMCPMedia(method string, next compositionHandler, audit app.AuditLog) compositionHandler {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var p struct {
			Origin    string `json:"origin"`
			Chat      string `json:"chat"`
			MessageID string `json:"messageId"`
		}
		_ = json.Unmarshal(raw, &p) // next validates; a malformed body is its error to report
		res, err := next(ctx, raw)
		if p.Origin != "mcp" {
			return res, err
		}
		decision := "ok"
		if err != nil {
			decision = "error"
		}
		chat, _ := domain.Parse(p.Chat)
		detail := "method=" + method
		if p.MessageID != "" {
			detail += " messageId=" + p.MessageID
		}
		evt := domain.NewAuditEventFrom("mcp", "mcp", domain.AuditMediaRead, chat, decision, detail)
		if aerr := audit.Record(ctx, evt); aerr != nil {
			return nil, fmt.Errorf("audit %s: %w", method, aerr)
		}
		return res, err
	}
}
