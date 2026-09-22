package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ethpandaops/benchmarkoor/pkg/compute"
	"github.com/ethpandaops/benchmarkoor/pkg/config"
)

func runComputeBuild(cfg *config.ComputeConfig) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validating compute config: %w", err)
	}

	ctx, stop := computeCommandContext()
	defer stop()

	artifactPath, err := compute.Build(ctx, log, cfg)
	if artifactPath != "" {
		log.WithField("workload", artifactPath).Info("Compute workload build finished")
	}
	if err != nil {
		return fmt.Errorf("building compute workload: %w", err)
	}

	return nil
}

func runComputeCampaign(cfg *config.ComputeConfig) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validating compute config: %w", err)
	}

	ctx, stop := computeCommandContext()
	defer stop()

	runDir, err := compute.Run(ctx, log, cfg)
	if runDir != "" {
		log.WithField("run_dir", runDir).Info("Compute campaign artifacts retained")
	}
	if err != nil {
		return fmt.Errorf("running compute campaign: %w", err)
	}

	return nil
}

func runComputeAnalysis(runDir, configOverride string) error {
	ctx, stop := computeCommandContext()
	defer stop()

	attemptDir, err := compute.Analyze(ctx, log, runDir, configOverride)
	if attemptDir != "" {
		log.WithField("analysis_dir", attemptDir).Info("Compute analysis artifacts retained")
	}
	if err != nil {
		return fmt.Errorf("analyzing compute run %q: %w", runDir, err)
	}

	return nil
}

func computeCommandContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-signals:
			log.WithField("signal", sig).Info("Received shutdown signal, cancelling compute command")
			cancel()
		case <-done:
		}
	}()

	return ctx, func() {
		signal.Stop(signals)
		close(done)
		cancel()
	}
}
