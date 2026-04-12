package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
)

func makeSigner() (jws.Signer2, error) {
	return jws.SignerFor(jwa.RS256())
}
