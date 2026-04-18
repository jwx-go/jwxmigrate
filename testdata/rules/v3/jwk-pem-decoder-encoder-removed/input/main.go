package example

import "github.com/lestrrat-go/jwx/v3/jwk"

func parsePEM(data []byte) (jwk.Set, error) {
	dec := jwk.NewPEMDecoder()
	_ = dec
	return jwk.Parse(data, jwk.WithPEMDecoder(nil))
}
