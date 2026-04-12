package example

import (
	"github.com/lestrrat-go/jwx/v2/jwe"
)

func decryptStandalone() {
	_ = jwe.WithLegacyHeaderMerging(true)
}
