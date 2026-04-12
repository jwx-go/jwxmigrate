package example

import (
	myjwk "github.com/lestrrat-go/jwx/v4/jwk"
	myjwt "github.com/lestrrat-go/jwx/v4/jwt"
)

func example(rawKey any) {
	key, _ := myjwk.Import(rawKey)
	tok, _ := myjwt.ParseFS("token.jwt")
	myjwt.RegisterCustomField("my-field", "")

	_ = key
	_ = tok
}
