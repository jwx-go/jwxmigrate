package example

import (
	"github.com/lestrrat-go/jwx/v3/jwt/openid"
)

var _ openid.Token

func standard() any {
	return openid.StandardClaimsFilter()
}
