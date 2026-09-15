package app

import (
	"context"
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

type scopedQuoteFixture struct {
	chat  domain.JID
	calls []domain.JID
	err   error
}

func (s *scopedQuoteFixture) GetRawProto(_ context.Context, chat domain.JID, _ domain.MessageID) ([]byte, error) {
	s.calls = append(s.calls, chat)
	if s.err != nil {
		return nil, s.err
	}
	if chat != s.chat {
		return nil, ErrMessageNotFound
	}
	return []byte("synthetic same-person quote"), nil
}

func TestQuotedReplyUsesOnlyKnownIdentityCounterpart(t *testing.T) {
	pn, lid := domain.MustJID("11111111@s.whatsapp.net"), domain.MustJID("22222222@lid")
	for _, reverse := range []bool{false, true} {
		from, stored := pn, lid
		if reverse {
			from, stored = lid, pn
		}
		resolver := newStubResolver()
		if err := resolver.RecordMapping(context.Background(), pn, lid); err != nil {
			t.Fatal(err)
		}
		quote := &scopedQuoteFixture{chat: stored}
		d := &Dispatcher{quoted: quote, identity: resolver}
		got, err := d.loadQuotedRaw(context.Background(), from, "QUOTE")
		if err != nil || string(got) != "synthetic same-person quote" {
			t.Fatalf("known alias: %q, %v", got, err)
		}
		if len(quote.calls) != 2 || quote.calls[0] != from || quote.calls[1] != stored {
			t.Fatalf("lookup escaped known pair: %v", quote.calls)
		}
	}
}

func TestQuotedReplyDoesNotGuessUnknownAliasOrHideStoreFailure(t *testing.T) {
	pn := domain.MustJID("11111111@s.whatsapp.net")
	quote := &scopedQuoteFixture{chat: domain.MustJID("22222222@lid")}
	d := &Dispatcher{quoted: quote, identity: newStubResolver()}
	if _, err := d.loadQuotedRaw(context.Background(), pn, "QUOTE"); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("unknown alias: %v", err)
	}
	if len(quote.calls) != 1 {
		t.Fatalf("unknown alias guessed: %v", quote.calls)
	}
	quote.calls, quote.err = nil, context.Canceled
	if _, err := d.loadQuotedRaw(context.Background(), pn, "QUOTE"); !errors.Is(err, context.Canceled) {
		t.Fatalf("store error lost: %v", err)
	}
	if len(quote.calls) != 1 {
		t.Fatalf("store error triggered fallback: %v", quote.calls)
	}
}
