package example

import (
	"github.com/lestrrat-go/jwx/v2/jwt"
)

func example(key any) {
	_ = jwt.WithDecrypt(key)
}
