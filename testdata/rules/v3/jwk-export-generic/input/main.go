package example

import (
	"crypto/rsa"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func export(key jwk.Key) error {
	var raw rsa.PrivateKey
	return jwk.Export(key, &raw)
}
