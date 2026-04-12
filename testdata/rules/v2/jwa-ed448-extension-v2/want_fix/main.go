package example

import (
	ed448 "github.com/jwx-go/ed448/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

// In v2 jwa.Ed448 was a constant value; in v4 it is a function call
// ed448.Ed448Curve(). The scanner rewrites the package and symbol name
// correctly, but the shape change (value → function call) is NOT
// automatic: the fixed output refers to the function by value rather
// than invoking it. Users must audit and append parentheses themselves.

func curve() jwa.EllipticCurveAlgorithm {
	return ed448.Ed448Curve
}
