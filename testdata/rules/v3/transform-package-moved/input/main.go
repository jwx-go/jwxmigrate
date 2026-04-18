package example

import "github.com/lestrrat-go/jwx/v3/transform"

func useTransform(m transform.Mappable) error {
	dst := map[string]any{}
	return transform.AsMap(m, dst)
}
