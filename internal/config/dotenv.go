// Package config 配置加载（P30）：零依赖 .env 加载器。
//
// 背景：此前 zebra 只用 os.Getenv 读系统环境变量，仓库根目录的 .env
// 对程序是"惰性"的——不 export/source 就不生效，README 的说法与实际
// 行为存在偏差。本包补齐最小 dotenv：启动时读取 .env，仅填充"尚未设置"
// 的变量。**真实环境变量优先，.env 只提供本地默认值**（CI/生产注入的
// 变量不会被 .env 覆盖）。
//
// 解析规则（刻意精简，够用且好懂）：
//
//	KEY=VALUE           赋值（key/value 首尾空白自动裁剪）
//	export KEY=VALUE    bash 风格前缀，同样支持
//	# 注释 / 空行       忽略
//	'VALUE' / "VALUE"   去掉包裹引号（不做变量插值）
//
// 生产演化方向：${VAR} 插值、多文件合并（.env.local > .env）、按 profile
// 切换；生产环境仍建议以 KMS/Vault 注入真实密钥，.env 仅作本地开发便利。
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Parse 解析 dotenv 文本为 map（纯函数，不写环境变量，便于测试）。
func Parse(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue // 空行与注释
		}
		if strings.HasPrefix(line, "export") {
			line = strings.TrimSpace(line[len("export"):]) // bash 风格前缀
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("第 %d 行格式错误（应为 KEY=VALUE）: %q", lineNo, line)
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			return nil, fmt.Errorf("第 %d 行 key 为空", lineNo)
		}
		out[key] = unquote(strings.TrimSpace(line[idx+1:]))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// unquote 去掉成对的单引号/双引号（值内部不做任何展开）。
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Load 读取并应用 path 中的变量：仅设置环境里"尚未存在"的变量。
// 返回实际设置的变量数（被系统环境变量占用的项不计入）。
func Load(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	vars, err := Parse(data)
	if err != nil {
		return 0, err
	}
	n := 0
	for k, v := range vars {
		if os.Getenv(k) == "" { // 真实环境变量优先，不覆盖
			_ = os.Setenv(k, v)
			n++
		}
	}
	return n, nil
}

// LoadDefault 加载当前工作目录下的 .env；文件不存在时视为"无配置"静默返回。
func LoadDefault() (int, error) {
	n, err := Load(".env")
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	return n, err
}
