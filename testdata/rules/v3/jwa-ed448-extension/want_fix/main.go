package example

import (
	ed448 "github.com/jwx-go/ed448/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

func curve() jwa.EllipticCurveAlgorithm {
	return ed448.Curve()
}
