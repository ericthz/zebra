//go:build !darwin && !linux

// 其他平台暂不支持 raw 模式：ReadLine 自动回退标准行读取（规范模式）。
package console

import "errors"

func makeRaw(fd int) (func(), error) {
	return nil, errors.New("raw mode not supported on this platform")
}
