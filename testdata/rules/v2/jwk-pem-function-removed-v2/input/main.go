package example

import "github.com/lestrrat-go/jwx/v2/jwk"

func emit(k jwk.Key) ([]byte, error) {
	return jwk.Pem(k)
}
