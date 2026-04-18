package example

import (
	"encoding/pem"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

type myKey struct{}

type myDecoder struct{}

func (myDecoder) Decode(block *pem.Block) (any, error) { return &myKey{}, nil }

var _ jwk.X509Decoder = myDecoder{}

var _ jwk.X509Decoder = jwk.X509DecodeFunc(func(block *pem.Block) (any, error) {
	return &myKey{}, nil
})
