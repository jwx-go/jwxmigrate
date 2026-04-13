package example

import (
	"github.com/lestrrat-go/jwx/v2/jwa"
)

// In v2 jwa.Ed448 was a constant value; in v4 it is a function call
// ed448.Curve(). The scanner rewrites the package and symbol name
// correctly, but the shape change (value → function call) is NOT
// automatic: the fixed output refers to the function by value rather
// than invoking it. Users must audit and append parentheses themselves.

func curve() jwa.EllipticCurveAlgorithm {
	return jwa.Ed448
}
