package example

import (
	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
)

func makeSigner() (jws.Signer, error) {
	return jws.SignerFor(jwa.RS256())
}
