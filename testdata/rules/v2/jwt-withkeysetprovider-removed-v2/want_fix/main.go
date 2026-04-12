package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func example() {
	_ = jwt.WithKeySetProvider(nil)
}
