package example

import (
	"github.com/lestrrat-go/jwx/v4/jwe"
)

// v2 had (jwe.Message).Decrypt(); v4 removed it in favor of the
// package-level jwe.Decrypt(). The rule pattern is `.Decrypt\(` so
// any .Decrypt call matches — realistic v2 usage calls it on a Message.

func unwrap(msg *jwe.Message, key any) ([]byte, error) {
	return msg.Decrypt(key)
}
