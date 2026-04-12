package example

import (
	"github.com/lestrrat-go/jwx/v2/jws"
)

func example() {
	_ = jws.WithPayloadSigner(nil)
}
