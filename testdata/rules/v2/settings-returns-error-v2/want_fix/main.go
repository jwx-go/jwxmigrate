package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func init() {
	jwt.Settings(jwt.WithMaxParseInputSize(1 << 20))
}
