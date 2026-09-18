// 日志文件：Zebra CLI 与 server 共用的日志文件打开逻辑。
//
// 约定（与 ZEBRA_LOG / LOG_FILE 语义一致）：
//
//	path 为具体路径 → 追加写该文件，返回关闭函数
//	path 为 "off"  → 禁用文件日志（返回 nil writer，由调用方决定回退目标）
package config

import (
	"io"
	"os"
)

// OpenLogFile 打开追加写日志文件；path=="off" 时返回 (nil, noop, nil)。
func OpenLogFile(path string) (io.Writer, func(), error) {
	if path == "off" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
