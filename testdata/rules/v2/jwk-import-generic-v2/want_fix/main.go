package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

func example(raw any) {
	key, _ := jwk.Import(raw)
	_ = key
}
