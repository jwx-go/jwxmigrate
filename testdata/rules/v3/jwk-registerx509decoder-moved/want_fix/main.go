package example

import (
	"encoding/pem"

	"github.com/lestrrat-go/jwx/v4/jwk"
)

type myKey struct{}

func init() {
	jwk.RegisterX509Decoder(&myKey{}, jwk.X509DecodeFunc(func(b *pem.Block) (any, error) {
		return &myKey{}, nil
	}))
}
