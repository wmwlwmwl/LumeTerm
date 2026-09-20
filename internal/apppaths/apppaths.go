package apppaths

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	once     sync.Once
	dataRoot string
)

// DataRoot 返回应用数据根目录。首次调用触发旧目录（Lumin）→ 新目录（LumeTerm）
// 的一次性迁移；迁移失败（文件被占用等）时回退旧目录，下次启动自动重试。
// ponytail: 同盘 rename 不复制数据；UserConfigDir 恒定同盘，无跨盘分支。
func DataRoot() string {
	once.Do(migrate)
	return dataRoot
}

func migrate() {
	base, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			dataRoot = "LumeTerm"
			return
		}
		base = home
	}
	dataRoot = filepath.Join(base, "LumeTerm")
	legacy := filepath.Join(base, "Lumin")
	if _, err := os.Stat(dataRoot); err == nil {
		return // 新目录已存在：不覆盖
	}
	if _, err := os.Stat(legacy); err != nil {
		return // 全新用户：无旧目录
	}
	if err := os.Rename(legacy, dataRoot); err != nil {
		log.Printf("[LumeTerm] 数据目录迁移失败，本次回退旧目录: %v", err)
		dataRoot = legacy
	}
}
