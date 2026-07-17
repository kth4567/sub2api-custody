package service

// owner_earning_worker.go —— 账号托管市场 模块 C：结算/释放后台任务。
// 结构参照 IdempotencyCleanupService（Start/Stop/runLoop + ticker + once 保护）。
// 需在 App 生命周期启动时调用 Start()、关闭时调用 Stop()（见 runbook「worker 接线」）。

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	// 结算轮询间隔。
	ownerEarningSettleInterval = 30 * time.Second
	// 每多少个结算周期做一次冻结释放扫描（20 * 30s ≈ 10 分钟）。
	ownerEarningReleaseEvery = 20
)

// OwnerEarningWorker 周期驱动号主收益结算与冻结释放。
type OwnerEarningWorker struct {
	svc      *OwnerEarningService
	interval time.Duration

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
}

// NewOwnerEarningWorker 构造后台任务（Wire provider）。
func NewOwnerEarningWorker(svc *OwnerEarningService) *OwnerEarningWorker {
	return &OwnerEarningWorker{
		svc:      svc,
		interval: ownerEarningSettleInterval,
		stopCh:   make(chan struct{}),
	}
}

// Start 幂等启动后台循环。
func (w *OwnerEarningWorker) Start() {
	if w == nil || w.svc == nil {
		return
	}
	w.startOnce.Do(func() {
		logger.LegacyPrintf("service.owner_earning", "[OwnerEarning] worker started interval=%s", w.interval)
		go w.runLoop()
	})
}

// Stop 幂等停止后台循环。
func (w *OwnerEarningWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
		logger.LegacyPrintf("service.owner_earning", "[OwnerEarning] worker stopped")
	})
}

func (w *OwnerEarningWorker) runLoop() {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var tick int
	w.tickOnce(&tick) // 启动即跑一轮，追平重启期间积压

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.tickOnce(&tick)
		}
	}
}

func (w *OwnerEarningWorker) tickOnce(tick *int) {
	ctx := context.Background()
	if err := w.svc.SettleOnce(ctx); err != nil {
		logger.LegacyPrintf("service.owner_earning", "[OwnerEarning] settle error: %v", err)
	}
	*tick++
	if *tick%ownerEarningReleaseEvery == 0 {
		if err := w.svc.ReleaseMaturedFrozen(ctx); err != nil {
			logger.LegacyPrintf("service.owner_earning", "[OwnerEarning] release error: %v", err)
		}
	}
}
