package example

import (
	"github.com/lestrrat-go/jwx/v2/jwk"
)

func example(data []byte) {
	key, _ := jwk.ParseKey(data)
	_ = key
}
