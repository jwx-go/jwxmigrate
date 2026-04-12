package example

import (
	"github.com/lestrrat-go/jwx/v2/jws"
)

// The wildcard pattern `jws\.Is\w+Error\(` matches every IsXxxError
// family member. Multiple calls in one file test that each is reported
// independently.

func classify(err error) {
	if jws.IsVerificationError(err) {
		return
	}
	if jws.IsSignatureError(err) {
		return
	}
	if jws.IsUnsupportedAlgorithmError(err) {
		return
	}
}
