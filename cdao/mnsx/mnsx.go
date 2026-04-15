package mnsx

import (
	ali_mns "github.com/aliyun/aliyun-mns-go-sdk"

	"github.com/micoya/gocraft/cdao"
)

// Must 返回指定名称的 ali_mns.MNSClient，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) ali_mns.MNSClient {
	return cdao.Must[ali_mns.MNSClient](d, "mns", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
