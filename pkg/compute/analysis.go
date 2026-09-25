package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ethpandaops/benchmarkoor/pkg/config"
	"github.com/ethpandaops/benchmarkoor/pkg/docker"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

const (
	analysisRootDir       = "analysis"
	analysisStatusFile    = "status.json"
	analysisContainerLog  = "analyzer.log"
	analysisContainerPath = "/campaign"
)

type analysisArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type analysisAttemptStatus struct {
	AttemptID     string             `json:"attempt_id"`
	Status        string             `json:"status"`
	Error         string             `json:"error,omitempty"`
	InputHashes   map[string]string  `json:"input_hashes,omitempty"`
	AnalyzerImage string             `json:"analyzer_image,omitempty"`
	ExitCode      *int64             `json:"exit_code,omitempty"`
	OOMKilled     *bool              `json:"oom_killed,omitempty"`
	Artifacts     []analysisArtifact `json:"artifacts"`
}

// Analyze creates one immutable evm-gasfit analysis attempt from an already
// recorded compute campaign. It never changes the campaign's raw workload,
// measurement, manifest, or archived analysis configuration bytes.
func Analyze(ctx context.Context, log logrus.FieldLogger, runDir string, configOverride string) (string, error) {
	attemptDir, attemptID, err := newAnalysisAttempt(runDir)
	if err != nil {
		return attemptDir, err
	}
	status := analysisAttemptStatus{
		AttemptID: attemptID,
		Status:    "running",
		Artifacts: []analysisArtifact{},
	}
	if err := writeAnalysisStatus(attemptDir, status); err != nil {
		return attemptDir, err
	}
	if err := updateComputeAnalysis(runDir, status); err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}

	campaign, err := readArchivedCampaign(filepath.Join(runDir, "campaign.json"))
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}
	timeout, err := campaign.TimeoutDuration()
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("parsing archived compute timeout: %w", err))
	}
	analysisCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	configPath, err := selectedAnalysisConfig(runDir, configOverride)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}

	inputs, err := exportGasfitInputs(runDir, attemptDir, configPath)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}
	status.InputHashes = inputs.Hashes
	if err := config.ValidateComputeQualificationPolicy(filepath.Join(attemptDir, gasfitConfigFile), campaign.Engine); err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}

	manager, err := newComputeManager(log, campaign.ContainerRuntime)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("creating analyzer container manager: %w", err))
	}
	if err := manager.Start(analysisCtx); err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("starting analyzer container manager: %w", err))
	}
	defer func() {
		if stopErr := manager.Stop(); stopErr != nil {
			log.WithError(stopErr).Warn("Stopping analyzer container manager failed")
		}
	}()

	imageReference, err := resolveComputeImage(analysisCtx, manager, campaign.Analyzer.Image)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("resolving analyzer image: %w", err))
	}
	status.AnalyzerImage = imageReference
	if err := writeAnalysisStatus(attemptDir, status); err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}

	containerRunDir, err := containerRelativeRunDir(runDir, attemptDir)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}
	runMount, err := filepath.Abs(runDir)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("resolving campaign path %q: %w", runDir, err))
	}
	reportsMount, err := filepath.Abs(filepath.Join(attemptDir, "reports"))
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("resolving analysis report path: %w", err))
	}
	if err := os.MkdirAll(reportsMount, 0o755); err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("creating analysis report directory: %w", err))
	}
	limits, err := computeResourceLimits(campaign.ResourceLimits)
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("resolving analyzer resource limits: %w", err))
	}
	args := []string{
		"run",
		"--config", filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, gasfitConfigFile)),
		"--runtimes", filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, gasfitRuntimesFile)),
		"--opcounts", filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, gasfitOpcountsFile)),
		"--manifest", filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, gasfitManifestFile)),
		"--out", filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, "reports")),
	}
	spec := &docker.ContainerSpec{
		Name:           "benchmarkoor-compute-analyze-" + attemptID,
		Image:          imageReference,
		Command:        args,
		ResourceLimits: limits,
		Mounts: []docker.Mount{
			{Type: "bind", Source: runMount, Target: analysisContainerPath, ReadOnly: true},
			{Type: "bind", Source: reportsMount, Target: filepath.ToSlash(filepath.Join(analysisContainerPath, containerRunDir, "reports"))},
		},
		Labels: map[string]string{
			"benchmarkoor.managed-by": "benchmarkoor",
			"benchmarkoor.compute":    "analysis",
			"benchmarkoor.attempt-id": attemptID,
		},
	}

	exit, runErr := runComputeContainer(analysisCtx, manager, spec, filepath.Join(attemptDir, analysisContainerLog))
	if exit != nil {
		status.ExitCode = &exit.ExitCode
		status.OOMKilled = &exit.OOMKilled
	}
	if runErr != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("running evm-gasfit: %w", runErr))
	}
	if exit.OOMKilled || exit.ExitCode != 0 {
		return finishAnalysis(runDir, attemptDir, status, "failed", fmt.Errorf("evm-gasfit exited with code %d (oom_killed=%t)", exit.ExitCode, exit.OOMKilled))
	}

	outcome, err := gasfitOutcome(filepath.Join(attemptDir, "reports", "analysis_status.json"))
	if err != nil {
		return finishAnalysis(runDir, attemptDir, status, "failed", err)
	}
	return finishAnalysis(runDir, attemptDir, status, outcome, nil)
}

func newAnalysisAttempt(runDir string) (string, string, error) {
	for range 10 {
		attemptID := uuid.NewString()
		attemptDir := filepath.Join(runDir, analysisRootDir, attemptID)
		if err := os.MkdirAll(filepath.Dir(attemptDir), 0o755); err != nil {
			return attemptDir, attemptID, fmt.Errorf("creating analysis directory: %w", err)
		}
		if err := os.Mkdir(attemptDir, 0o755); err == nil {
			return attemptDir, attemptID, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return attemptDir, attemptID, fmt.Errorf("creating analysis attempt %q: %w", attemptDir, err)
		}
	}
	return "", "", errors.New("creating unique analysis attempt: UUID collision limit reached")
}

func readArchivedCampaign(path string) (*config.ComputeConfig, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading archived campaign %q: %w", path, err)
	}
	var campaign config.ComputeConfig
	if err := json.Unmarshal(contents, &campaign); err != nil {
		return nil, fmt.Errorf("decoding archived campaign %q: %w", path, err)
	}
	if campaign.Analyzer.Image == "" {
		return nil, fmt.Errorf("archived campaign %q has no analyzer image", path)
	}
	if campaign.Engine == "" {
		return nil, fmt.Errorf("archived campaign %q names no engine", path)
	}
	return &campaign, nil
}

func selectedAnalysisConfig(runDir, configOverride string) (string, error) {
	path := filepath.Join(runDir, "analysis-config.yaml")
	if configOverride != "" {
		path = configOverride
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving analysis config %q: %w", path, err)
	}
	if info, err := os.Stat(absolute); err != nil {
		return "", fmt.Errorf("reading analysis config %q: %w", absolute, err)
	} else if !info.Mode().IsRegular() {
		return "", fmt.Errorf("analysis config %q is not a regular file", absolute)
	}
	return absolute, nil
}

func writeAnalysisStatus(attemptDir string, status analysisAttemptStatus) error {
	status.Artifacts = analysisArtifacts(attemptDir)
	return writeComputeJSON(filepath.Join(attemptDir, analysisStatusFile), status)
}

func finishAnalysis(runDir, attemptDir string, status analysisAttemptStatus, outcome string, analysisErr error) (string, error) {
	status.Status = outcome
	if analysisErr != nil {
		status.Error = analysisErr.Error()
	}
	status.Artifacts = analysisArtifacts(attemptDir)
	if err := writeAnalysisStatus(attemptDir, status); err != nil {
		return attemptDir, fmt.Errorf("recording analysis attempt status after %v: %w", analysisErr, err)
	}
	if err := updateComputeAnalysis(runDir, status); err != nil {
		if analysisErr != nil {
			return attemptDir, fmt.Errorf("%w; updating compute analysis summary: %v", analysisErr, err)
		}
		return attemptDir, fmt.Errorf("updating compute analysis summary: %w", err)
	}
	return attemptDir, analysisErr
}

func updateComputeAnalysis(runDir string, status analysisAttemptStatus) error {
	path := filepath.Join(runDir, "config.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading run summary %q: %w", path, err)
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(contents, &summary); err != nil {
		return fmt.Errorf("decoding run summary %q: %w", path, err)
	}
	computeRaw, ok := summary["compute"]
	if !ok {
		return fmt.Errorf("run summary %q has no compute section", path)
	}
	var compute map[string]json.RawMessage
	if err := json.Unmarshal(computeRaw, &compute); err != nil {
		return fmt.Errorf("decoding compute section in %q: %w", path, err)
	}
	analysisRaw, err := json.Marshal(struct {
		Status    string             `json:"status"`
		AttemptID string             `json:"attempt_id"`
		Artifacts []analysisArtifact `json:"artifacts"`
	}{
		Status: status.Status, AttemptID: status.AttemptID, Artifacts: status.Artifacts,
	})
	if err != nil {
		return fmt.Errorf("encoding compute analysis summary: %w", err)
	}
	compute["analysis"] = analysisRaw
	updatedCompute, err := json.Marshal(compute)
	if err != nil {
		return fmt.Errorf("encoding compute summary: %w", err)
	}
	summary["compute"] = updatedCompute
	return writeComputeJSON(path, summary)
}

func analysisArtifacts(attemptDir string) []analysisArtifact {
	artifacts := make([]analysisArtifact, 0)
	runDir := filepath.Dir(filepath.Dir(attemptDir))
	_ = filepath.WalkDir(attemptDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == filepath.Join(attemptDir, analysisStatusFile) {
			return nil
		}
		relative, relErr := filepath.Rel(runDir, path)
		if relErr == nil {
			artifacts = append(artifacts, analysisArtifact{Name: filepath.Base(path), Path: filepath.ToSlash(relative)})
		}
		return nil
	})
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts
}

func containerRelativeRunDir(runDir, attemptDir string) (string, error) {
	relative, err := filepath.Rel(runDir, attemptDir)
	if err != nil {
		return "", fmt.Errorf("deriving analysis container path: %w", err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("analysis attempt %q is not inside run directory %q", attemptDir, runDir)
	}
	return relative, nil
}

func gasfitOutcome(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading evm-gasfit analysis status %q: %w", path, err)
	}
	var status struct {
		PlannedModels []struct {
			Status                 string `json:"status"`
			AdjustedEstimateStatus string `json:"adjusted_estimate_status"`
		} `json:"planned_models"`
	}
	if err := json.Unmarshal(contents, &status); err != nil {
		return "", fmt.Errorf("decoding evm-gasfit analysis status %q: %w", path, err)
	}
	if len(status.PlannedModels) == 0 {
		return "inconclusive", nil
	}
	hasFailed := false
	hasUnqualified := false
	for _, model := range status.PlannedModels {
		for _, qualificationStatus := range []string{
			model.Status,
			model.AdjustedEstimateStatus,
		} {
			if qualificationStatus == "failed" {
				hasFailed = true
			}
			if qualificationStatus != "qualified" {
				hasUnqualified = true
			}
		}
	}
	if hasFailed {
		return "failed", nil
	}
	if hasUnqualified {
		return "inconclusive", nil
	}
	return "succeeded", nil
}
