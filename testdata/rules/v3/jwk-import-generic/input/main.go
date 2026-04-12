package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
)

func example(rawKey any) {
	key, _ := jwk.Import(rawKey)
	_ = key
}
