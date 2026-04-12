package example

import (
	"github.com/lestrrat-go/jwx/v4/jwe"
)

func encode(payload []byte, key any) ([]byte, error) {
	return jwe.JSON(payload, key)
}
