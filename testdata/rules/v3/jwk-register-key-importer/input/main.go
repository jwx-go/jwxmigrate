package example

import (
	"errors"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

type myKeyType struct{}

func register() {
	jwk.RegisterKeyImporter(&myKeyType{}, jwk.KeyImportFunc(func(raw any) (jwk.Key, error) {
		if _, ok := raw.(*myKeyType); !ok {
			return nil, errors.New("unexpected type")
		}
		return nil, nil
	}))
}
