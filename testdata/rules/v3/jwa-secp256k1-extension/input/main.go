package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
)

func curve() jwa.EllipticCurveAlgorithm {
	return jwa.Secp256k1()
}
