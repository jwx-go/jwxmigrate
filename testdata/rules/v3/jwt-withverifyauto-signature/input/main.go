package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func parse(signed []byte, wl jwk.Whitelist) (jwt.Token, error) {
	return jwt.Parse(signed, jwt.WithVerifyAuto(nil, jwk.WithFetchWhitelist(wl)))
}
