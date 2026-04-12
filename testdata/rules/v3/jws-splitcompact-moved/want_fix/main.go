package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jws/jwsbb"
)

func split(buf []byte) ([]byte, []byte, []byte, error) {
	return jwsbb.SplitCompact(buf)
}
