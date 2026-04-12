package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

func useImport(rawKey any) {
	key, _ := jwk.Import(rawKey)
	_ = key
}
