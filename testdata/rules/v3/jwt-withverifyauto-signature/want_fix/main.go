package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func parse(signed []byte, wl jwk.Whitelist) (jwt.Token, error) {
	return jwt.Parse(signed, jwt.WithVerifyAuto(nil, jwk.WithFetchWhitelist(wl)))
}
