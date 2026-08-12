// tool/datetime.go
package tool

import (
	"time"
)

type DateTimeTool struct{}

func (d *DateTimeTool) Name() string { return "get_current_datetime" }

func (d *DateTimeTool) Description() string {
	return "获取当前日期和时间，返回 ISO 8601 格式。"
}

func (d *DateTimeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (d *DateTimeTool) Execute(args map[string]interface{}) (string, error) {
	now := time.Now()
	return now.Format("2006-01-02 15:04:05 Monday"), nil
}
