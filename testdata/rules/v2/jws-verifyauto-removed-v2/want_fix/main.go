package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

func verify(payload []byte) ([]byte, error) {
	return jws.VerifyAuto(payload)
}
