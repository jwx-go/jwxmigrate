package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

type mySigner struct{}

var _ jws.Signer = mySigner{}

func (mySigner) Sign(key any, payload []byte) ([]byte, error) { return nil, nil }
