package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

// Realistic v3 signature: a helper that takes ReadFileOption to forward
// to jwt.ReadFile. In v4 this becomes ParseOption (both the type
// reference in the parameter list and any zero-value use). The rule
// carries replacement=ParseOption so the fixer renames the type in
// place.

func load(path string, opts ...jwt.ParseOption) error {
	_, err := jwt.ReadFile(path, opts...)
	return err
}
