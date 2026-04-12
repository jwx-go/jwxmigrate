package example

import (
	"github.com/lestrrat-go/jwx/v2/jwe"
)

func classify(err error) {
	if jwe.IsDecryptError(err) {
		return
	}
	if jwe.IsEncryptError(err) {
		return
	}
}
