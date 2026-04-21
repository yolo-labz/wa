package domain

import "errors"

// QuoteChainMaxDepth caps quote nesting (FR-070). Quotes beyond this depth
// MUST be flattened at adapter boundaries. WhatsApp itself tolerates
// arbitrary nesting on the wire; capping here keeps payload size bounded
// and stops quote-loop amplification attacks.
const QuoteChainMaxDepth = 5

// ErrQuoteChainTooDeep signals that a chain of QuotedMessage parents
// exceeds QuoteChainMaxDepth. Returned by ValidateQuoteChain.
var ErrQuoteChainTooDeep = errors.New("quote chain exceeds max depth 5")

// ValidateQuoteChain walks the Parent pointers of chain and returns an
// error if the chain depth exceeds QuoteChainMaxDepth. A nil chain is
// depth 0 and valid. The function is pure and alloc-free.
func ValidateQuoteChain(chain *QuotedMessageChain) error {
	depth := 0
	for n := chain; n != nil; n = n.Parent {
		depth++
		if depth > QuoteChainMaxDepth {
			return ErrQuoteChainTooDeep
		}
	}
	return nil
}

// QuotedMessageChain is a linked-list node wrapping QuotedMessage to
// allow bounded-depth quote traversal without introducing a cycle in
// the domain type graph.
type QuotedMessageChain struct {
	Message QuotedMessage
	Parent  *QuotedMessageChain
}

// Depth returns the chain length including c itself. Nil chain is 0.
func (c *QuotedMessageChain) Depth() int {
	d := 0
	for n := c; n != nil; n = n.Parent {
		d++
	}
	return d
}
