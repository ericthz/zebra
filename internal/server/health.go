// B8 健康检查 + B5 指标暴露。
//
//	/healthz 存活探针（进程活着即 200）
//	/readyz  就绪探针（依赖可用才 200，供 k8s/负载均衡摘流）
//	/metrics Prometheus 文本格式（计数器 + 直方图），可被抓取
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// HealthzHandler 存活探针。
func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}
}

// ReadyzHandler 就绪探针：逐个检查依赖（LLM、Qdrant 等），全部通过才 200。
func ReadyzHandler(logger *slog.Logger, deps map[string]func() error) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		var failed []string
		for name, check := range deps {
			if err := check(); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", name, err))
				logger.Warn("ready check failed", "dep", name, "err", err)
			}
		}
		if len(failed) > 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "unready: %s", strings.Join(failed, "; "))
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ready")
	}
}

// Metrics 轻量指标：计数器 + 时长直方图，Prometheus 文本格式。
type Metrics struct {
	mu     sync.Mutex
	count  map[string]int64
	latSum map[string]float64 // 累计秒数
	latN   map[string]int64   // 样本数
}

// NewMetrics 构造。
func NewMetrics() *Metrics {
	return &Metrics{
		count: make(map[string]int64), latSum: make(map[string]float64), latN: make(map[string]int64),
	}
}

// Inc 计数器 +1。
func (m *Metrics) Inc(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count[name]++
}

// Observe 记录一次耗时。
func (m *Metrics) Observe(name string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latSum[name] += d.Seconds()
	m.latN[name]++
}

// Handler 输出 Prometheus 文本。
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		names := make([]string, 0, len(m.count))
		for k := range m.count {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(w, "zebra_%s %d\n", strings.ReplaceAll(n, ":", "_"), m.count[n])
		}
		for _, n := range names {
			if m.latN[n] == 0 {
				continue
			}
			fmt.Fprintf(w, "zebra_%s_seconds_sum %f\n", strings.ReplaceAll(n, ":", "_"), m.latSum[n])
			fmt.Fprintf(w, "zebra_%s_seconds_count %d\n", strings.ReplaceAll(n, ":", "_"), m.latN[n])
		}
	}
}
