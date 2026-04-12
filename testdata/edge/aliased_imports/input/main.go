package example

import (
	myjwk "github.com/lestrrat-go/jwx/v3/jwk"
	myjwt "github.com/lestrrat-go/jwx/v3/jwt"
)

func example(rawKey any) {
	key, _ := myjwk.Import(rawKey)
	tok, _ := myjwt.ReadFile("token.jwt")
	myjwt.RegisterCustomField("my-field", "")

	_ = key
	_ = tok
}
