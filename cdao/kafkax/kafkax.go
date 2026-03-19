package kafkax

import (
	"github.com/micoya/gocraft/cdao"
	"github.com/micoya/gocraft/cdao/provider/kafka"
)

// Must 返回指定名称的 *kafka.Client，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) *kafka.Client {
	return cdao.Must[*kafka.Client](d, "kafka", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
