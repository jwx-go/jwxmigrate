package example

import (
	"github.com/lestrrat-go/jwx/v2/jws"
)

func verify(payload []byte) ([]byte, error) {
	return jws.VerifyAuto(payload)
}
