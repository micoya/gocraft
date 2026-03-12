package mcachex

import (
	"github.com/dgraph-io/ristretto/v2"

	"github.com/micoya/gocraft/cdao"
)

// Must 返回指定名称的 *ristretto.Cache[string, any]，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) *ristretto.Cache[string, any] {
	return cdao.Must[*ristretto.Cache[string, any]](d, "mcache", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
