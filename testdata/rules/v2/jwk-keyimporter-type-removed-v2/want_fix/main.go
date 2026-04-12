package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

type myImporter struct{}

func (myImporter) Import(_ any) (jwk.Key, error) { return nil, nil }

var _ jwk.KeyImporter = myImporter{}

func register() {
	jwk.RegisterKeyImporter(myImporter{}, jwk.KeyImportFunc(func(any) (jwk.Key, error) {
		return nil, nil
	}))
}
