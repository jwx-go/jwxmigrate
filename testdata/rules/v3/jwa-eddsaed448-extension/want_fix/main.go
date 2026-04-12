package example

import (
	ed448 "github.com/jwx-go/ed448/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

func algorithm() jwa.SignatureAlgorithm {
	return ed448.EdDSAEd448()
}
