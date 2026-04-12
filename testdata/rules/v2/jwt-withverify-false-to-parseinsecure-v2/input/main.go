package example

import (
	"github.com/lestrrat-go/jwx/v2/jwt"
)

// v2 jwt.ParseString(data, jwt.WithVerify(false)) maps to v4
// jwt.ParseInsecure([]byte(data)). The tool recognizes the
// jwt.WithVerify(false) argument via the existing fixWithVerifyFalse
// rewriter.
//
// KNOWN GAP: deriveSignatureChange extracts only the v2 field
// ("ParseString") as the matcher name, not every search_patterns entry.
// So this rule currently fires only on ParseString calls, missing Parse
// calls (which have the same v2/v4 mapping). The Parse call on line 17
// below is captured in the golden verbatim (unmodified) to surface this
// gap — it should be regenerated when deriveSignatureChange learns to
// extract multiple names like deriveRemovedOrMoved does.

func insecure(data string) (jwt.Token, error) {
	return jwt.ParseString(data, jwt.WithVerify(false))
}

func insecureBytes(data []byte) (jwt.Token, error) {
	return jwt.Parse(data, jwt.WithVerify(false))
}
