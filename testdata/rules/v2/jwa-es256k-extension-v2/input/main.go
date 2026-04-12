package example

import (
	"github.com/lestrrat-go/jwx/v2/jwa"
)

func algorithm() jwa.SignatureAlgorithm {
	return jwa.ES256K()
}
