package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
)

func makeVerifier() (jws.Verifier2, error) {
	return jws.VerifierFor(jwa.RS256())
}
