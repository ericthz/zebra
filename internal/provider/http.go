// 可靠 HTTP 客户端（B7 熔断/降级/容错 + B9 错误恢复）
//
// 三件套：超时 → 重试（指数退避）→ 熔断。
// 纯标准库实现，足够演示生产级容错骨架；真实场景可在此之上换用
// 官方 SDK 或增强型客户端，接口不变。
package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// HTTPClient 封装带容错的 HTTP 客户端。
type HTTPClient struct {
	client     *http.Client
	maxRetries int
	backoff    time.Duration // 首次退避基数，之后 2^n 指数增长
	breaker    *CircuitBreaker
}

// NewHTTPClient 构造。timeout 为单次请求超时；maxRetries 为失败重试次数。
func NewHTTPClient(timeout time.Duration, maxRetries int, backoff time.Duration) *HTTPClient {
	return &HTTPClient{
		client:     &http.Client{Timeout: timeout},
		maxRetries: maxRetries,
		backoff:    backoff,
		breaker:    NewCircuitBreaker(5, 30*time.Second), // 连续 5 次失败熔断 30s
	}
}

// Do 发送带 JSON body 的请求；仅对"可重试"错误（网络错误、5xx）重试。
// setAuth 用于按厂商注入鉴权头。
func (c *HTTPClient) Do(ctx context.Context, method, url string, payload []byte, setAuth func(*http.Request)) (*http.Response, error) {
	if !c.breaker.Allow() {
		return nil, fmt.Errorf("熔断器已打开：%s 暂不可用", url)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.backoff << uint(attempt-1) // 指数退避
			select {
			case <-ctx.Done():
				return nil, ctx.Err() // B9: 上下文取消/超时传播
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if setAuth != nil {
			setAuth(req)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err // 网络错误 → 可重试
			continue
		}
		if resp.StatusCode >= 500 { // 服务端错误 → 可重试
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		c.breaker.Success() // 成功 → 复位熔断
		return resp, nil
	}

	c.breaker.Failure()
	return nil, fmt.Errorf("请求失败（已重试 %d 次）: %w", c.maxRetries, lastErr)
}

// CircuitBreaker 简单熔断器：连续失败达阈值则打开，冷却期后进入半开探测。
type CircuitBreaker struct {
	mu        sync.Mutex
	failures  int
	open      bool
	openedAt  time.Time
	threshold int
	cooldown  time.Duration
}

// NewCircuitBreaker 构造。
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{threshold: threshold, cooldown: cooldown}
}

// Allow 是否放行请求。
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.open && time.Since(b.openedAt) > b.cooldown {
		b.open = false // 半开：放一个探测请求
		b.failures = 0
	}
	return !b.open
}

// Success 记录成功。
func (b *CircuitBreaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.open = false
}

// Failure 记录失败，达到阈值打开熔断。
func (b *CircuitBreaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.open = true
		b.openedAt = time.Now()
	}
}
