package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/inkronik/kubernetes-agent/internal/collector"
	"github.com/inkronik/kubernetes-agent/internal/config"
	"github.com/inkronik/kubernetes-agent/internal/health"
	"github.com/inkronik/kubernetes-agent/internal/k8s"
	"github.com/inkronik/kubernetes-agent/internal/runtime"
	"github.com/inkronik/kubernetes-agent/internal/sender"
	"github.com/inkronik/kubernetes-agent/internal/version"
)

func main() {
	if shouldPrintVersion(os.Args) {
		fmt.Fprintln(os.Stdout, version.Value)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", slog.Any("error", err))
		os.Exit(1)
	}

	k8sClient, err := k8s.New(cfg.KubeconfigPath)
	if err != nil {
		logger.Error("failed to create kubernetes client", slog.Any("error", err))
		os.Exit(1)
	}

	cluster := collector.ClusterMetadata{
		Name:         cfg.ClusterName,
		Provider:     cfg.ClusterProvider,
		Environment:  cfg.Environment,
		AgentVersion: cfg.AgentVersion,
	}

	collectorInstance := collector.New(collector.Options{
		Client:             k8sClient,
		Cluster:            cluster,
		Namespaces:         cfg.Namespaces,
		EventTypes:         cfg.EventTypes,
		EnableKubeletStats: cfg.KubeletStatsEnabled,
	})
	senderInstance := sender.New(sender.ClientOptions{
		BaseURL:       cfg.CollectorURL,
		APIKey:        cfg.IngestAPIKey,
		ApplicationID: cfg.ApplicationID,
		Timeout:       cfg.RequestTimeout,
	})
	runner := runtime.New(cfg, collectorInstance, senderInstance, logger)
	healthServer := health.New(cfg.HealthAddress, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	healthServer.Start()
	healthServer.MarkReady()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout)
		defer cancel()
		if err := healthServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("health server shutdown failed", slog.Any("error", err))
		}
	}()

	logger.Info(
		"starting inkronik-k8s-agent",
		slog.String("agentVersion", cfg.AgentVersion),
		slog.String("cluster", cfg.ClusterName),
		slog.String("environment", cfg.Environment),
		slog.String("collectorUrl", cfg.CollectorURL),
	)

	if err := runner.Run(ctx); err != nil {
		logger.Error("agent exited with error", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("inkronik-k8s-agent stopped")
}

func shouldPrintVersion(args []string) bool {
	if len(args) != 2 {
		return false
	}

	return args[1] == "--version" || args[1] == "version"
}
