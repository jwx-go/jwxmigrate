package sample

import (
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func example() {
	// Get pattern (should trigger get-to-field)
	var sub string
	_ = token.Get(jwt.SubjectKey, &sub)

	// ReadFile pattern (should trigger readfile-to-parsefs)
	tok, _ := jwt.ReadFile("token.jwt")
	set, _ := jwk.ReadFile("keys.json")

	// Import pattern (should trigger jwk-import-generic)
	key, _ := jwk.Import(rawKey)

	// ParseKey pattern (should trigger jwk-parsekey-generic)
	parsed, _ := jwk.ParseKey(data)

	// RegisterCustomField pattern
	jwt.RegisterCustomField("my-field", time.Time{})

	// Signer2 reference (should trigger jws-signer2-to-signer)
	var s jws.Signer2

	// Cache usage (should trigger jwk-cache-removed)
	cache, _ := jwk.NewCache(ctx, client)

	// DecoderSettings (should trigger remove-decodersettings)
	jwx.DecoderSettings(jwx.WithUseNumber(true))

	_ = fmt.Sprintf("%v %v %v %v %v %v", tok, set, key, parsed, s, cache)
}
