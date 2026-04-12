package example

// Package-level doc comment must be preserved verbatim.

import (
	// Leading comment on the import group.
	"github.com/lestrrat-go/jwx/v3/jws" // trailing comment
)

// example demonstrates a rename that must not disturb surrounding comments.
//
// Note: this is a multi-line doc comment that must survive the fix.
func example() {
	// Inline comment before the declaration.
	var s jws.Signer2 // end-of-line comment
	_ = s
	// Trailing comment inside the function.
}
