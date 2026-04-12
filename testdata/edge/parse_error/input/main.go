package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
)

// Deliberate syntax error: missing closing brace. The scanner must skip
// this file gracefully and emit no findings (not even the import-change
// rule, because parseGoFile returns nil on parse errors).

func broken( {
	_ = jwk.Import
