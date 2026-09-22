package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ethpandaops/benchmarkoor/pkg/docker"
	"github.com/ethpandaops/benchmarkoor/pkg/podman"
	"github.com/sirupsen/logrus"
)

const computeStopTimeoutSeconds = 15

// newComputeManager selects the configured local container runtime. The caller
// starts and stops the returned manager around its campaign or analysis attempt.
func newComputeManager(log logrus.FieldLogger, runtimeName string) (docker.ContainerManager, error) {
	switch runtimeName {
	case "", "docker":
		manager, err := docker.NewManager(log.WithField("component", "compute.docker"))
		if err != nil {
			return nil, fmt.Errorf("creating docker compute manager: %w", err)
		}

		return manager, nil
	case "podman":
		manager, err := podman.NewManager(log.WithField("component", "compute.podman"))
		if err != nil {
			return nil, fmt.Errorf("creating podman compute manager: %w", err)
		}

		return manager, nil
	default:
		return nil, fmt.Errorf("unsupported compute container runtime %q", runtimeName)
	}
}

// Container runtimes expose either a registry digest or a local image ID.
// Inspect the immutable reference before using it; these are not interchangeable.
func resolveComputeImage(ctx context.Context, manager docker.ContainerManager, image string) (string, error) {
	digest, err := manager.GetImageDigest(ctx, image)
	if err != nil {
		if err := manager.PullImage(ctx, image, "if-not-present"); err != nil {
			return "", fmt.Errorf("pulling image %q: %w", image, err)
		}
		digest, err = manager.GetImageDigest(ctx, image)
	}
	if err != nil {
		return "", fmt.Errorf("inspecting image %q: %w", image, err)
	}
	if digest == "" {
		return "", fmt.Errorf("image %q has no immutable identity", image)
	}
	if _, err := manager.GetImageDigest(ctx, digest); err == nil {
		return digest, nil
	}
	reference := strings.SplitN(image, "@", 2)[0] + "@" + digest
	if _, err := manager.GetImageDigest(ctx, reference); err != nil {
		return "", fmt.Errorf("inspecting immutable image %q: %w", reference, err)
	}
	return reference, nil
}

// runComputeContainer executes one finite worker or analyzer container. It
// retains complete combined logs, records the runtime's terminal exit facts,
// stops a cancelled container, and always removes it before returning.
func runComputeContainer(
	ctx context.Context,
	manager docker.ContainerManager,
	spec *docker.ContainerSpec,
	logPath string,
) (exit *docker.ContainerExitInfo, retErr error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return exit, fmt.Errorf("creating container log directory: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return exit, fmt.Errorf("opening container log %q: %w", logPath, err)
	}
	defer func() {
		if closeErr := logFile.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("closing container log: %w", closeErr)
		}
	}()

	containerID, err := manager.CreateContainer(ctx, spec)
	if err != nil {
		return exit, fmt.Errorf("creating compute container %q: %w", spec.Name, err)
	}
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancelCleanup()
		if removeErr := manager.RemoveContainer(cleanupCtx, containerID); removeErr != nil && retErr == nil {
			retErr = fmt.Errorf("removing compute container %q: %w", spec.Name, removeErr)
		}
	}()

	if err := manager.StartContainer(ctx, containerID); err != nil {
		return exit, fmt.Errorf("starting compute container %q: %w", spec.Name, err)
	}

	streamCtx, stopStream := context.WithCancel(ctx)
	var streamErr error
	completed := false
	var streamWG sync.WaitGroup
	streamWG.Add(1)
	go func() {
		defer streamWG.Done()
		streamErr = manager.StreamLogs(streamCtx, containerID, logFile, logFile)
	}()
	defer func() {
		if !completed {
			stopStream()
		}
		streamWG.Wait()
		stopStream()
		if streamErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("streaming compute container logs: %w", streamErr))
		}
	}()

	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	statusCh, errCh := manager.WaitForContainerExit(waitCtx, containerID)

	select {
	case status, ok := <-statusCh:
		if !ok {
			return exit, fmt.Errorf("waiting for compute container %q: status channel closed", spec.Name)
		}
		exit = &status
		completed = true
		if exit.ExitCode != 0 || exit.OOMKilled {
			return exit, fmt.Errorf("compute container %q exited with code %d (oom_killed=%t)", spec.Name, exit.ExitCode, exit.OOMKilled)
		}

		return exit, nil
	case waitErr, ok := <-errCh:
		if !ok || waitErr == nil {
			return exit, fmt.Errorf("waiting for compute container %q: wait channel closed", spec.Name)
		}

		return exit, fmt.Errorf("waiting for compute container %q: %w", spec.Name, waitErr)
	case <-ctx.Done():
		stopCtx, cancelStop := context.WithTimeout(context.Background(), time.Duration(computeStopTimeoutSeconds+5)*time.Second)
		defer cancelStop()
		if err := manager.StopContainer(stopCtx, containerID, intPointer(computeStopTimeoutSeconds)); err != nil {
			_, _ = fmt.Fprintf(logFile, "\nbenchmarkoor: stopping cancelled container: %v\n", err)
		}

		select {
		case status, ok := <-statusCh:
			if ok {
				exit = &status
			}
		case waitErr := <-errCh:
			if waitErr != nil {
				_, _ = fmt.Fprintf(logFile, "\nbenchmarkoor: wait after cancellation: %v\n", waitErr)
			}
		case <-stopCtx.Done():
			_, _ = fmt.Fprintln(logFile, "benchmarkoor: container exit was not observed before cleanup")
		}

		return exit, fmt.Errorf("compute container %q cancelled: %w", spec.Name, ctx.Err())
	}
}

// writeComputeJSON atomically persists a controller-owned JSON snapshot. It
// writes a same-directory temporary file so observers never see a partial JSON
// document.
func writeComputeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON for %q: %w", path, err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating JSON directory for %q: %w", path, err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".compute-*.json")
	if err != nil {
		return fmt.Errorf("creating JSON temporary file for %q: %w", path, err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("writing JSON temporary file for %q: %w", path, err)
	}
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return fmt.Errorf("setting JSON permissions for %q: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncing JSON temporary file for %q: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("closing JSON temporary file for %q: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("installing JSON snapshot %q: %w", path, err)
	}

	return nil
}

func intPointer(value int) *int {
	return &value
}

func writeComputeJSONLine(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling JSONL record: %w", err)
	}
	if _, err := writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing JSONL record: %w", err)
	}

	return nil
}
