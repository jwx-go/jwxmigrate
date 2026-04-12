package example

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jws"
)

// Realistic v3 legacy signer / verifier registration. What actually fires
// today on this fixture:
//
//   - jws-register-signer              — jws.RegisterSigner(...)
//   - jws-register-verifier            — jws.RegisterVerifier(...)
//   - jws-signerfactory-removed        — jws.SignerFactory   (type reference)
//   - jws-verifierfactory-removed      — jws.VerifierFactory (type reference)
//   - jws-withlegacysigners-removed    — jws.WithLegacySigners()
//   - jws-signer2-to-signer            — jws.Signer2   return type
//   - jws-verifier2-to-verifier        — jws.Verifier2 return type
//
// KNOWN SCANNER GAP: the following rules target identifiers used here as
// call expressions (jws.SignerFactoryFn(fn), jws.SignerAdapter(v), and
// their verifier counterparts). Their search patterns omit `\(`, so
// ast_derive builds a SelectorExpr matcher only — and the scanner's
// SelectorExpr branch explicitly skips nodes that are the Fun of a
// CallExpr. Result: the calls below are silently missed today:
//
//   - jws-signerfactoryfn-removed
//   - jws-signeradapter-removed
//   - jws-verifierfactoryfn-removed
//   - jws-verifideradapter-removed
//
// When ast_derive emits both CallExpr and SelectorExpr matchers for
// kindRemoved (or the search patterns gain `\(`), regenerate this golden
// and the four missed rules will light up.

type legacySigner struct{}

func (legacySigner) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256() }
func (legacySigner) Sign([]byte, any) ([]byte, error)  { return nil, nil }

type legacyVerifier struct{}

func (legacyVerifier) Algorithm() jwa.SignatureAlgorithm { return jwa.RS256() }
func (legacyVerifier) Verify([]byte, []byte, any) error  { return nil }

func registerLegacy() {
	var factory jws.SignerFactory = jws.SignerFactoryFn(func() (jws.Signer2, error) {
		return jws.SignerAdapter(legacySigner{}), nil
	})
	_ = jws.RegisterSigner(jwa.RS256(), factory)

	var vf jws.VerifierFactory = jws.VerifierFactoryFn(func() (jws.Verifier2, error) {
		return jws.VerifierAdapter(legacyVerifier{}), nil
	})
	_ = jws.RegisterVerifier(jwa.RS256(), vf)
}

func verifyWithLegacy(tok []byte) ([]byte, error) {
	return jws.Verify(tok, jws.WithLegacySigners())
}
