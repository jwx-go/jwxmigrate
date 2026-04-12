package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
)

// Realistic v3 usage of both jwk.KeyImporter (as an interface type in a
// variable declaration) and jwk.KeyImportFunc (as a constructor call
// passed to RegisterKeyImporter). The rule fires on both forms; the
// yaml rule has no v4 replacement, so fix is a no-op modulo the import
// path rewrite.

type myImporter struct{}

func (myImporter) Import(_ any) (jwk.Key, error) { return nil, nil }

var _ jwk.KeyImporter = myImporter{}

func register() {
	jwk.RegisterKeyImporter(myImporter{}, jwk.KeyImportFunc(func(any) (jwk.Key, error) {
		return nil, nil
	}))
}
