package example

import (
	"errors"

	"github.com/lestrrat-go/jwx/v2/jwt"
)

func check(err error) bool {
	return errors.Is(err, jwt.ErrMissingRequiredClaim)
}
