package example

import (
	"github.com/lestrrat-go/jwx/v4/jwe"
)

// v2 had DecryptEncryptOption, v4 renamed to EncryptDecryptOption
// (producer before consumer).

func take(opts ...jwe.EncryptDecryptOption) {
	_ = opts
}
