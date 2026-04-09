package sample

import (
	"fmt"
	"os"

	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func example() {
	// v4 field access
	v, ok := key.Field("kid")

	// v4 generic Get
	kid, _ := jwk.Get[string](key, "kid")

	// v4 ParseFS
	tok, _ := jwt.ParseFS(os.DirFS("."), "token.jwt")

	// v4 generic Import
	rsaKey, _ := jwk.Import[jwk.RSAPrivateKey](rawKey)

	// v4 generic RegisterCustomField
	jwt.RegisterCustomField[string]("my-field")

	_ = fmt.Sprintf("%v %v %v %v", v, ok, kid, tok, rsaKey)
}
