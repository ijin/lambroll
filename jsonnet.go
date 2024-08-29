package lambroll

import (
	"fmt"
	"os"

	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
)

func DefaultJsonnetNativeFuncs() []*jsonnet.NativeFunction {
	return []*jsonnet.NativeFunction{
		{
			Name:   "env",
			Params: []ast.Identifier{"name", "default"},
			Func: func(args []any) (any, error) {
				key, ok := args[0].(string)
				if !ok {
					return nil, fmt.Errorf("env: name must be a string")
				}
				if v := os.Getenv(key); v != "" {
					return v, nil
				}
				return args[1], nil
			},
		},
		{
			Name:   "must_env",
			Params: []ast.Identifier{"name"},
			Func: func(args []any) (any, error) {
				key, ok := args[0].(string)
				if !ok {
					return nil, fmt.Errorf("must_env: name must be a string")
				}
				if v, ok := os.LookupEnv(key); ok {
					return v, nil
				}
				return nil, fmt.Errorf("must_env: %s is not set", key)
			},
		},
	}
}
