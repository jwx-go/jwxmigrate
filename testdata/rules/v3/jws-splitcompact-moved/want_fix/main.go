package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

func split(buf []byte) ([]byte, []byte, []byte, error) {
	return jws.SplitCompact(buf)
}
