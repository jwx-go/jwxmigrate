package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
)

// Behavioral rule: jws.WithKey() now validates algorithm/key compatibility
// at construction time instead of at Sign/Verify. Existing call sites keep
// compiling; they may start erroring earlier if the alg/key pair is wrong.

func signPayload(key any, payload []byte) ([]byte, error) {
	return jws.Sign(payload, jws.WithKey(jwa.RS256(), key))
}

func verifyPayload(key any, payload []byte) ([]byte, error) {
	return jws.Verify(payload, jws.WithKey(jwa.RS256(), key))
}
