package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

func verify(payload []byte, key any) ([]byte, error) {
	return jws.Verify(payload, jws.WithVerify(nil, key))
}
