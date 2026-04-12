package example

import (
	"github.com/lestrrat-go/jwx/v4/jws"
)

func example() {
	_ = jws.WithPayloadSigner(nil)
}
