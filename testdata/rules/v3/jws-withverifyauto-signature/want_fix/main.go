package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jws"
)

func verify(signed []byte, wl jwk.Whitelist) ([]byte, error) {
	return jws.Verify(signed, jws.WithVerifyAuto(nil, jwk.WithFetchWhitelist(wl)))
}
