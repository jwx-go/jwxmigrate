package example

import (
	"github.com/lestrrat-go/jwx/v4/jwe"
)

func classify(err error) {
	if jwe.IsDecryptError(err) {
		return
	}
	if jwe.IsEncryptError(err) {
		return
	}
}
