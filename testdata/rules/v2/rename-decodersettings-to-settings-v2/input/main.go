package example

import (
	"github.com/lestrrat-go/jwx/v2"
)

func init() {
	jwx.DecoderSettings(jwx.WithUseNumber(true))
}
