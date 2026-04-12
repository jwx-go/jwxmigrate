package example

import (
	"time"

	"github.com/lestrrat-go/jwx/v3/jwt"
)

func init() {
	jwt.RegisterCustomField("my-timestamp", time.Time{})
	jwt.RegisterCustomField("my-duration", time.Duration(0))
}
