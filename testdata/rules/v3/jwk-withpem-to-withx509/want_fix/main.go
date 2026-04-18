package example

import "github.com/lestrrat-go/jwx/v4/jwk"

func parsePEM(data []byte) (jwk.Set, error) {
	return jwk.Parse(data, jwk.WithX509(true))
}
