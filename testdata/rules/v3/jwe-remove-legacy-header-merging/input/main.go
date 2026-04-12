package example

import (
	"github.com/lestrrat-go/jwx/v3/jwe"
)

// jwe.WithLegacyHeaderMerging is removed in v4. Realistic use is as an
// option to jwe.Decrypt or jwe.Encrypt. The rule fires on the call site;
// the fixer tries fixDeleteStatement, which only succeeds when the call
// is a standalone statement. Nested in another call, the finding is
// reported but the surrounding call is left for the user to rewrite.

func decryptStandalone() {
	_ = jwe.WithLegacyHeaderMerging(true)
}
