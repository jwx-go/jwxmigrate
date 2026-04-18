package example

import "github.com/lestrrat-go/jwx/v4/jwk"

type myKey struct{}

func cleanup() {
	jwk.UnregisterX509Decoder(&myKey{})
}
