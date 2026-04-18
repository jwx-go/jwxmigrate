package example

import (
	"github.com/jwx-go/jwxfilter/v4/openidfilter"
	"github.com/lestrrat-go/jwx/v4/jwt/openid"
)

var _ openid.Token

func standard() any {
	return openidfilter.Standard()
}
