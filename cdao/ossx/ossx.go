package ossx

import (
	alioss "github.com/aliyun/aliyun-oss-go-sdk/oss"

	"github.com/micoya/gocraft/cdao"
)

// Must 返回指定名称的 *oss.Client，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) *alioss.Client {
	return cdao.Must[*alioss.Client](d, "oss", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
