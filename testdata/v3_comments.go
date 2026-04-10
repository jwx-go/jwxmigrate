package sample

import (
	"fmt"

	_ "github.com/lestrrat-go/jwx/v3/jwk"
)

// This file imports v3 but all API usage is in comments or strings.
// Only the import itself should produce findings.

func commentExample() {
	// jwk.Import(rawKey) — this is a comment, not a call
	// jwk.NewCache(ctx, client)
	// token.Get(jwt.SubjectKey, &sub)
	// jwt.ReadFile("token.jwt")

	s := "jwk.Import(something)"
	s2 := `jwk.ParseKey(data)`

	_ = fmt.Sprintf("%s %s", s, s2)
}
