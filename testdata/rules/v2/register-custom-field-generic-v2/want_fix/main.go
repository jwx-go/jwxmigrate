package example

import (
	"time"

	"github.com/lestrrat-go/jwx/v4/jwt"
)

func init() {
	jwt.RegisterCustomField("my-timestamp", time.Time{})
}
