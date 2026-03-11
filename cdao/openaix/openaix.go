package openaix

import (
	gopenai "github.com/openai/openai-go"

	"github.com/micoya/gocraft/cdao"
)

// Must 返回指定名称的 *openai.Client，不存在或类型不匹配时 panic。
// name 可省略，省略时使用 "default"。
func Must(d *cdao.DAO, name ...string) *gopenai.Client {
	return cdao.Must[*gopenai.Client](d, "openai", resolveName(name))
}

func resolveName(names []string) string {
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return "default"
}
