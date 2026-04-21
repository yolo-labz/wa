package domain

import (
	"errors"
	"fmt"
	"time"
)

// ErrReceipt categorises MessageReceipt shape errors.
var ErrReceipt = errors.New("domain: invalid message receipt")

// MessageReceipt is the value object recording one delivery/read/played
// receipt for a previously sent or observed message. ReceiptKind mirrors
// ReceiptStatus but as a string-typed enum suitable for the `kind` column
// of `messages.db` v3 `message_receipts` (data-model §MessageReceipt).
type MessageReceipt struct {
	MessageID MessageID
	Kind      ReceiptStatus
	TS        time.Time
	ByJID     JID // zero for the "sent" case and for self-receipts
}

// Validate enforces: non-zero message id, valid kind, non-zero timestamp.
func (r MessageReceipt) Validate() error {
	if r.MessageID.IsZero() {
		return fmt.Errorf("%w: message_id is zero", ErrReceipt)
	}
	if !r.Kind.IsValid() {
		return fmt.Errorf("%w: kind %d", ErrReceipt, r.Kind)
	}
	if r.TS.IsZero() {
		return fmt.Errorf("%w: ts is zero", ErrReceipt)
	}
	return nil
}
