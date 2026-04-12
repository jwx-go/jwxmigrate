package example

import (
	"io/fs"

	"github.com/lestrrat-go/jwx/v2/jwt"
)

func example(fsys fs.FS) {
	_ = jwt.WithFS(fsys)
}
