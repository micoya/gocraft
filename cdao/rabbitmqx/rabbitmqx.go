package rabbitmqx

import (
	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/micoya/gocraft/cdao"
)

// Must 返回指定名称的 *amqp.Connection，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) *amqp.Connection {
	return cdao.Must[*amqp.Connection](d, "rabbitmq", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
