package example

import (
	"github.com/lestrrat-go/jwx/v3"
)

func init() {
	jwx.DecoderSettings(jwx.WithUseNumber(true))
}
