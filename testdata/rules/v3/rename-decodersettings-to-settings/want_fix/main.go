package example

import (
	"github.com/lestrrat-go/jwx/v4"
)

func init() {
	jwx.Settings(jwx.WithUseNumber(true))
}
