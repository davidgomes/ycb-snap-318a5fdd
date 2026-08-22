//go:build go1.22
// +build go1.22

package stdlib

import (
	"reflect"

	"github.com/traefik/yaegi/embed"
)

func init() {
	Symbols["embed/embed"] = map[string]reflect.Value{
		"FS": reflect.ValueOf((*embed.FS)(nil)),
	}
}
