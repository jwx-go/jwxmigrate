package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

type myDecoder struct{}

func (myDecoder) Decode(_ []byte) (any, error) { return nil, nil }

func init() {
	jwt.RegisterCustomDecoder("my-field", myDecoder{})
}
