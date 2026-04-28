package domain_test

import (
	"errors"
	"testing"

	"github.com/yolo-labz/wa/v2/internal/domain"
)

// TestQuoteChainMaxDepth5 asserts ValidateQuoteChain enforces the FR-070
// depth cap of 5.
func TestQuoteChainMaxDepth5(t *testing.T) {
	t.Parallel()

	// nil chain: depth 0, valid.
	if err := domain.ValidateQuoteChain(nil); err != nil {
		t.Fatalf("nil chain: unexpected error %v", err)
	}

	build := func(depth int) *domain.QuotedMessageChain {
		var head *domain.QuotedMessageChain
		for range depth {
			head = &domain.QuotedMessageChain{Parent: head}
		}
		return head
	}

	// depth 1..5 valid.
	for d := 1; d <= domain.QuoteChainMaxDepth; d++ {
		if err := domain.ValidateQuoteChain(build(d)); err != nil {
			t.Errorf("depth %d: unexpected error %v", d, err)
		}
	}

	// depth 6 rejected.
	if err := domain.ValidateQuoteChain(build(6)); !errors.Is(err, domain.ErrQuoteChainTooDeep) {
		t.Fatalf("depth 6: want ErrQuoteChainTooDeep, got %v", err)
	}

	// depth 20 rejected.
	if err := domain.ValidateQuoteChain(build(20)); !errors.Is(err, domain.ErrQuoteChainTooDeep) {
		t.Fatalf("depth 20: want ErrQuoteChainTooDeep, got %v", err)
	}
}

// TestQuoteChainDepthReport asserts the Depth helper agrees with chain
// construction, exercising the null case.
func TestQuoteChainDepthReport(t *testing.T) {
	t.Parallel()

	var head *domain.QuotedMessageChain
	if got := head.Depth(); got != 0 {
		t.Fatalf("nil.Depth() = %d, want 0", got)
	}

	head = &domain.QuotedMessageChain{}
	head = &domain.QuotedMessageChain{Parent: head}
	head = &domain.QuotedMessageChain{Parent: head}
	if got := head.Depth(); got != 3 {
		t.Fatalf("head.Depth() = %d, want 3", got)
	}
}
