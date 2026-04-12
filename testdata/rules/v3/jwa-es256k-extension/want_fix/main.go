package example

import (
	es256k "github.com/jwx-go/es256k/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

func algorithm() jwa.SignatureAlgorithm {
	return es256k.ES256K()
}
