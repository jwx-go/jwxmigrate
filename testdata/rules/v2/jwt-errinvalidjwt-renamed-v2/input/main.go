package example

import (
	"errors"

	"github.com/lestrrat-go/jwx/v2/jwt"
)

// v2 jwt.ErrInvalidJWT → v4 jwt.UnknownPayloadTypeError(). The tool's
// rename target is a prose-like phrase ("UnknownPayloadTypeError") that
// is a valid Go identifier, so the selector-rename path would fire.
// Note: v4 made these functions instead of sentinels, so even after the
// selector rename the user still needs to audit the call shape.

func classify(err error) bool {
	return errors.Is(err, jwt.ErrInvalidJWT)
}
