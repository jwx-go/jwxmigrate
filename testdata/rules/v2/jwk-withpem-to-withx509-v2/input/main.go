package example

import "github.com/lestrrat-go/jwx/v2/jwk"

func parsePEM(data []byte) (jwk.Set, error) {
	return jwk.Parse(data, jwk.WithPEM(true))
}
