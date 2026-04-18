package example

import "github.com/lestrrat-go/jwx/v4/jwk"

func emit(k jwk.Key) ([]byte, error) {
	return jwk.EncodePEM(k)
}
