package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// Passing a v3 function as a value (not calling it) exercises the
// SelectorExpr matcher path independently of CallExpr.

func example() {
	f := jwk.Import
	_ = f
}
