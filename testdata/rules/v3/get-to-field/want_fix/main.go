package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// The get-to-field rule targets .Get(name, &dst) method calls on v3
// Token/Headers types. The tool can rewrite these into the generic free
// function form (e.g. jwt.Get[T](tok, name)) only when type-checked
// loading succeeds. Fixtures run without a go.mod, so pf.TypesInfo is
// nil and the fix path is skipped — the rule fires via Check only.

func readSubject(tok jwt.Token) string {
	var sub string
	_ = tok.Get(jwt.SubjectKey, &sub)
	return sub
}
