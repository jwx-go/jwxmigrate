package example

import (
	"github.com/lestrrat-go/jwx/v4"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// Several mechanical rules in one file. After running fix once:
//   - all v3 imports become v4
//   - Signer2 → Signer
//   - ReadFile → ParseFS (name-only rewrite)
//   - DecoderSettings → Settings
// A second fix pass must be a no-op and no v3 paths may remain.

type mySigner struct{}

var _ jws.Signer = mySigner{}

func (mySigner) Sign(key any, payload []byte) ([]byte, error) { return nil, nil }

func run() {
	tok, _ := jwt.ParseFS("token.jwt")
	_ = tok
	jwx.Settings(jwx.WithUseNumber(true))
}
