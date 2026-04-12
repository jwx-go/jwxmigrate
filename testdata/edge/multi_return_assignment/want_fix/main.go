package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// Multi-return destructuring forms must still match signature_change rules.

func example(rawKey any) {
	tok, _ := jwt.ParseFS("token.jwt")
	key, err := jwk.Import(rawKey)
	_, _ = tok, err
	_ = key
}
