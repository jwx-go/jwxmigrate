package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

type myVerifier struct{}

var _ jws.Verifier = myVerifier{}

func (myVerifier) Verify(key any, payload, signature []byte) error { return nil }
