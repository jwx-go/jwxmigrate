package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
)

func algorithm() jwa.SignatureAlgorithm {
	return jwa.EdDSAEd448()
}
