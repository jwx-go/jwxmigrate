package example

import (
	"github.com/lestrrat-go/jwx/v4/jwt"
)

func load(path string, opts ...jwt.ParseOption) error {
	_, err := jwt.ReadFile(path, opts...)
	return err
}
