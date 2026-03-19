package mongo

import (
	"go.mongodb.org/mongo-driver/event"
	"go.opentelemetry.io/contrib/instrumentation/go.mongodb.org/mongo-driver/mongo/otelmongo"
)

// newMonitor 创建 OTel CommandMonitor，为每条 MongoDB 命令（find、insert、update、delete 等）
// 自动生成 client span，记录命令名、数据库名、集合名等属性。
// 通过 mongo.Options.SetMonitor 注册到驱动层，业务层无需手动埋点。
func newMonitor() *event.CommandMonitor {
	return otelmongo.NewMonitor()
}
