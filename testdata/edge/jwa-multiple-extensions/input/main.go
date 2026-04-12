package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
)

// A file that uses symbols from both the es256k and ed448 extension
// packages. Each moved_to_extension rule rewrites both the selector
// package and (when applicable) the symbol name, and ensureExtensionImports
// injects one import per distinct extension module in a single post-pass.

func algorithms() []jwa.SignatureAlgorithm {
	return []jwa.SignatureAlgorithm{
		jwa.ES256K(),
		jwa.EdDSAEd448(),
	}
}

func curves() []jwa.EllipticCurveAlgorithm {
	return []jwa.EllipticCurveAlgorithm{
		jwa.Secp256k1(),
		jwa.Ed448(),
	}
}
