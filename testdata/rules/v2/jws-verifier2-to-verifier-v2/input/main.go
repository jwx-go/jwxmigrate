package example

import (
	"github.com/lestrrat-go/jwx/v2/jws"
)

type myVerifier struct{}

var _ jws.Verifier2 = myVerifier{}

func (myVerifier) Verify(key any, payload, signature []byte) error { return nil }
