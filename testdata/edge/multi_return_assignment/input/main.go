package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Multi-return destructuring forms must still match signature_change rules.

func example(rawKey any) {
	tok, _ := jwt.ReadFile("token.jwt")
	key, err := jwk.Import(rawKey)
	_, _ = tok, err
	_ = key
}
