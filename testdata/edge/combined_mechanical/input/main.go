package example

import (
	"github.com/lestrrat-go/jwx/v3"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Several mechanical rules in one file. After running fix once:
//   - all v3 imports become v4
//   - Signer2 → Signer
//   - ReadFile → ParseFS (name-only rewrite)
//   - DecoderSettings → Settings
// A second fix pass must be a no-op and no v3 paths may remain.

type mySigner struct{}

var _ jws.Signer2 = mySigner{}

func (mySigner) Sign(key any, payload []byte) ([]byte, error) { return nil, nil }

func run() {
	tok, _ := jwt.ReadFile("token.jwt")
	_ = tok
	jwx.DecoderSettings(jwx.WithUseNumber(true))
}
