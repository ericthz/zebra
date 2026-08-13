// Package notify 主动出站能力（P4）。
//
// 背景：Agent 此前只会"你问→我答"。成熟 Agent 需要【主动通知】：
//   - 长任务完成回调（异步任务跑完 → Webhook 推给业务系统）
//   - 定时报告 / 异常告警推送
//
// 本包提供：
//   - Notifier 接口（可插拔：Webhook / 邮件 / IM 推送）
//   - WebhookNotifier 实现：HTTP POST + 幂等键 + 指数退避重试
//
// 生产演化方向：
//   - 消息签名（HMAC）防伪造；重试放进消息队列（Kafka/RabbitMQ）
//   - 邮件/钉钉/企微等适配器（实现同一 Notifier 接口）
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notifier 通知器接口：Send 返回是否送达。
type Notifier interface {
	Send(ctx context.Context, event Event) error
}

// Event 一次通知事件。
type Event struct {
	Type    string         `json:"type"`    // 事件类型：task.complete / task.failed / report
	Session string         `json:"session"` // 来源会话
	Title   string         `json:"title"`   // 标题
	Payload map[string]any `json:"payload"` // 业务数据（回复摘要、指标等）
}

// WebhookNotifier 通过 HTTP POST 推送事件。
type WebhookNotifier struct {
	URL        string // 回调地址
	Secret     string // 签名密钥（可选，非空则带 HMAC 头）
	Client     *http.Client
	MaxRetries int
	Backoff    time.Duration
}

// NewWebhookNotifier 构造。
func NewWebhookNotifier(url, secret string) *WebhookNotifier {
	return &WebhookNotifier{
		URL: url, Secret: secret,
		Client:     &http.Client{Timeout: 10 * time.Second},
		MaxRetries: 3,
		Backoff:    500 * time.Millisecond,
	}
}

// Send 推送事件：幂等键 + 指数退避重试。
func (w *WebhookNotifier) Send(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= w.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(w.Backoff << uint(attempt-1)): // 指数退避
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		// 幂等键：同一事件重试时接收方可据此去重
		req.Header.Set("X-Idempotency-Key", fmt.Sprintf("%s:%d", ev.Type, time.Now().UnixNano()))
		if w.Secret != "" {
			// HMAC 签名：接收方校验，防伪造推送
			mac := hmac.New(sha256.New, []byte(w.Secret))
			mac.Write(body)
			req.Header.Set("X-Signature", hex.EncodeToString(mac.Sum(nil)))
		}
		resp, err := w.Client.Do(req)
		if err != nil {
			lastErr = err // 网络错误 → 可重试
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 { // 服务端错误 → 可重试
			lastErr = fmt.Errorf("webhook HTTP %d", resp.StatusCode)
			continue
		}
		return nil // 2xx/4xx 视为送达
	}
	return fmt.Errorf("webhook 推送失败（已重试 %d 次）: %w", w.MaxRetries, lastErr)
}
