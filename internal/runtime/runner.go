package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/inkronik/kubernetes-agent/internal/collector"
	"github.com/inkronik/kubernetes-agent/internal/config"
	"github.com/inkronik/kubernetes-agent/internal/sender"
)

type Runner struct {
	config          config.Config
	collector       *collector.Collector
	sender          *sender.Client
	logger          *slog.Logger
	lastEventCursor time.Time
}

func New(config config.Config, collector *collector.Collector, sender *sender.Client, logger *slog.Logger) *Runner {
	return &Runner{
		config:          config,
		collector:       collector,
		sender:          sender,
		logger:          logger,
		lastEventCursor: time.Now().UTC().Add(-config.InitialEventLookback),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	r.sendMetrics(ctx)
	r.sendEvents(ctx)

	metricsTicker := time.NewTicker(r.config.MetricsInterval)
	defer metricsTicker.Stop()

	eventsTicker := time.NewTicker(r.config.EventSyncInterval)
	defer eventsTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-metricsTicker.C:
			r.sendMetrics(ctx)
		case <-eventsTicker.C:
			r.sendEvents(ctx)
		}
	}
}

func (r *Runner) sendMetrics(ctx context.Context) {
	signals, err := r.collector.CollectMetrics(ctx)
	if err != nil {
		r.logger.Error("metric collection failed", slog.Any("error", err))
		return
	}

	if err := r.sender.SendTelemetry(ctx, signals); err != nil {
		r.logger.Error("metric send failed", slog.Any("error", err))
		return
	}

	r.logger.Info("metrics sent", slog.Int("signals", len(signals)))
}

func (r *Runner) sendEvents(ctx context.Context) {
	signals, latestSeen, err := r.collector.CollectEvents(ctx, r.lastEventCursor)
	if err != nil {
		r.logger.Error("event collection failed", slog.Any("error", err))
		return
	}

	if len(signals) == 0 {
		return
	}

	if err := r.sender.SendTelemetry(ctx, signals); err != nil {
		r.logger.Error("event send failed", slog.Any("error", err))
		return
	}

	r.lastEventCursor = latestSeen
	r.logger.Info("events sent", slog.Int("signals", len(signals)))
}
