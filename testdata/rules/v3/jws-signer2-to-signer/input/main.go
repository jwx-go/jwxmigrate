package example

import (
	"github.com/lestrrat-go/jwx/v3/jws"
)

type mySigner struct{}

var _ jws.Signer2 = mySigner{}

func (mySigner) Sign(key any, payload []byte) ([]byte, error) { return nil, nil }
