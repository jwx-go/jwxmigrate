package example

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"os"
)

// Lock in the path-split behavior for readfile-to-parsefs:
//
//   - "token.jwt"                → os.DirFS("."),       "token.jwt"
//   - "keys.json"                → os.DirFS("."),       "keys.json"
//   - "config/keys.json"         → os.DirFS("config"),  "keys.json"
//   - "/etc/jwx/token.jwt"       → os.DirFS("/etc/jwx"),"token.jwt"
//   - "/token.jwt"               → os.DirFS("/"),       "token.jwt"
//
// A dynamic path argument must NOT be auto-rewritten (the tool can't
// split a runtime string), so the variable form below falls through to
// a no-edit outcome and is reported as a remaining manual fix.

func static() {
	tok1, _ := jwt.ParseFS(os.DirFS("."), "token.jwt")
	set1, _ := jwk.ParseFS(os.DirFS("."), "keys.json")
	set2, _ := jwk.ParseFS(os.DirFS("config"), "keys.json")
	tok2, _ := jwt.ParseFS(os.DirFS("/etc/jwx"), "token.jwt")
	tok3, _ := jwt.ParseFS(os.DirFS("/"), "token.jwt")

	_ = tok1
	_ = set1
	_ = set2
	_ = tok2
	_ = tok3
}

func dynamic(path string) {
	tok, _ := jwt.ReadFile(path)
	_ = tok
}
