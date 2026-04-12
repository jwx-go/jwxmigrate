package example

import (
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
)

func makeVerifier() (jws.Verifier, error) {
	return jws.VerifierFor(jwa.RS256())
}
