package stdlib

import (
	"reflect"

	"github.com/traefik/yaegi/stdlib/embed"
)

func init() {
	Symbols["embed/embed"] = map[string]reflect.Value{
		"FS": reflect.ValueOf((*embed.FS)(nil)),
	}
}
