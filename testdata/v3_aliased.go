package sample

import (
	myjwk "github.com/lestrrat-go/jwx/v3/jwk"
	myjwt "github.com/lestrrat-go/jwx/v3/jwt"
)

func aliasedExample() {
	// These should trigger rules even with aliased imports.
	key, _ := myjwk.Import(rawKey)
	tok, _ := myjwt.ReadFile("token.jwt")
	myjwt.RegisterCustomField("my-field", "")

	_ = key
	_ = tok
}
