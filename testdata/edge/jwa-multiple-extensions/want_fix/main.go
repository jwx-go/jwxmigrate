package example

import (
	ed448 "github.com/jwx-go/ed448/v4"
	es256k "github.com/jwx-go/es256k/v4"
	"github.com/lestrrat-go/jwx/v4/jwa"
)

// A file that uses symbols from both the es256k and ed448 extension
// packages. Each moved_to_extension rule rewrites both the selector
// package and (when applicable) the symbol name, and ensureExtensionImports
// injects one import per distinct extension module in a single post-pass.

func algorithms() []jwa.SignatureAlgorithm {
	return []jwa.SignatureAlgorithm{
		es256k.ES256K(),
		ed448.EdDSAEd448(),
	}
}

func curves() []jwa.EllipticCurveAlgorithm {
	return []jwa.EllipticCurveAlgorithm{
		es256k.Secp256k1(),
		ed448.Ed448Curve(),
	}
}
