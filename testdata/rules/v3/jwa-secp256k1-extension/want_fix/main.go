package example

import (
	es256k "github.com/jwx-go/es256k/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

func curve() jwa.EllipticCurveAlgorithm {
	return es256k.Secp256k1()
}
