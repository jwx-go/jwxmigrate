package example

import "github.com/lestrrat-go/jwx/v3/jwk"

func parsePEM(data []byte) (jwk.Set, error) {
	return jwk.Parse(data, jwk.WithPEM(true))
}
