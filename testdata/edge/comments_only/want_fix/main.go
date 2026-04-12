package example

import (
	"fmt"

	_ "github.com/lestrrat-go/jwx/v4/jwk"
)

// This file imports v3 only as a blank import. All API references live in
// comments and string literals and must not trigger any API rule.

func example() {
	// jwk.Import(rawKey) — a comment, not a call
	// jwk.NewCache(ctx, client)
	// token.Get(jwt.SubjectKey, &sub)
	// jwt.ReadFile("token.jwt")

	s := "jwk.Import(something)"
	s2 := `jwk.ParseKey(data)`

	_ = fmt.Sprintf("%s %s", s, s2)
}
