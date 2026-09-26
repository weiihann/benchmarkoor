package compute

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ethpandaops/benchmarkoor/pkg/config"
	"github.com/ethpandaops/benchmarkoor/pkg/docker"
	"github.com/ethpandaops/benchmarkoor/pkg/executor"
	"github.com/ethpandaops/benchmarkoor/pkg/runner"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

const (
	computeMountPath       = "/campaign"
	computeWorkerLogName   = "container.log"
	computeAnalysisPending = "pending"
)

type plannedSession struct {
	Request Request
	Path    string
}

type campaignSummary struct {
	Timestamp         int64  `json:"timestamp"`
	TimestampEnd      int64  `json:"timestamp_end,omitempty"`
	SuiteHash         string `json:"suite_hash,omitempty"`
	Status            string `json:"status,omitempty"`
	TerminationReason string `json:"termination_reason,omitempty"`
	Instance          struct {
		ID               string `json:"id"`
		Client           string `json:"client"`
		Image            string `json:"image"`
		RollbackStrategy string `json:"rollback_strategy,omitempty"`
	} `json:"instance"`
	TestCounts struct {
		Total  int `json:"total"`
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	} `json:"test_counts"`
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Compute struct {
		SchemaVersion int    `json:"schema_version"`
		ManifestPath  string `json:"manifest_path"`
		SamplesPath   string `json:"samples_path"`
		Analysis      struct {
			Status    string `json:"status"`
			AttemptID string `json:"attempt_id,omitempty"`
			Artifacts []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"artifacts,omitempty"`
		} `json:"analysis"`
	} `json:"compute"`
}

// Build generates and validates an Osaka workload package, or validates and
// returns the configured pre-generated package.
func Build(ctx context.Context, log logrus.FieldLogger, cfg *config.ComputeConfig) (string, error) {
	if cfg == nil {
		return "", errors.New("compute configuration is required")
	}
	if cfg.Workload != "" {
		path, err := filepath.Abs(cfg.Workload)
		if err != nil {
			return "", fmt.Errorf("resolving workload path: %w", err)
		}
		if _, _, err := loadComputeWorkload(path); err != nil {
			return "", fmt.Errorf("validating configured workload %q: %w", path, err)
		}

		return path, nil
	}
	if cfg.Generator == nil {
		return "", errors.New("compute workload or generator is required")
	}

	return generateComputeWorkload(ctx, log, cfg)
}

// Run records an immutable campaign, measures every write-ahead request through
// fresh worker sessions, reconciles all terminal records, then invokes analysis.
// It returns the run directory even when measurement or analysis is attributable
// to the campaign rather than an inability to create its artifact directory.
func Run(ctx context.Context, log logrus.FieldLogger, cfg *config.ComputeConfig) (string, error) {
	if cfg == nil {
		return "", errors.New("compute configuration is required")
	}
	// The campaign engine fixes the boundary every reconciled result must
	// report; resolving it once here keeps later fabrication sites honest.
	boundary, err := ExecutionBoundaryForEngine(cfg.Engine)
	if err != nil {
		return "", fmt.Errorf("resolving compute engine boundary: %w", err)
	}
	resultsDir, err := filepath.Abs(cfg.ResultsDir)
	if err != nil {
		return "", fmt.Errorf("resolving compute results directory: %w", err)
	}
	runID := "compute-" + uuid.NewString()
	runDir := filepath.Join(resultsDir, "runs", runID)
	if err := os.MkdirAll(filepath.Join(runDir, "sessions"), 0o755); err != nil {
		return "", fmt.Errorf("creating compute run directory: %w", err)
	}
	workloadPath, err := Build(ctx, log, cfg)
	if err != nil {
		summary := newCampaignSummary(cfg, "")
		summary.Status = "failed"
		summary.TerminationReason = err.Error()
		summary.TimestampEnd = time.Now().Unix()
		if summaryErr := writeCampaignSummary(runDir, &summary); summaryErr != nil {
			return runDir, errors.Join(err, summaryErr)
		}

		return runDir, err
	}
	workload, workloadBytes, err := loadComputeWorkload(workloadPath)
	if err != nil {
		return runDir, fmt.Errorf("loading generated workload: %w", err)
	}
	workloadArchive := filepath.Join(runDir, "workload.json")
	if err := writeImmutableBytes(workloadArchive, workloadBytes); err != nil {
		return runDir, fmt.Errorf("archiving workload: %w", err)
	}
	workloadSHA := sha256Bytes(workloadBytes)
	summary := newCampaignSummary(cfg, workloadSHA)
	if err := writeCampaignSummary(runDir, &summary); err != nil {
		return runDir, err
	}

	campaign := *cfg
	campaign.ResultsDir = resultsDir
	if cfg.Analyzer != nil {
		analysisConfig, err := archiveAnalysisConfig(runDir, cfg.Analyzer.Config)
		if err != nil {
			return runDir, recordCampaignFailure(runDir, &summary, err)
		}
		// Copy before rewriting: campaign shares the caller's pointer.
		analyzer := *cfg.Analyzer
		analyzer.Config = analysisConfig
		campaign.Analyzer = &analyzer
	}
	archivedWorkload, err := filepath.Abs(workloadArchive)
	if err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("resolving archived workload: %w", err))
	}
	campaign.Workload = archivedWorkload
	if cfg.Generator != nil {
		generator := *cfg.Generator
		campaign.Generator = nil
		if err := writeComputeJSON(filepath.Join(runDir, "generator.json"), &generator); err != nil {
			return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("archiving generator configuration: %w", err))
		}
	}
	if err := writeComputeJSON(filepath.Join(runDir, "campaign.json"), &campaign); err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("writing campaign configuration: %w", err))
	}

	manager, err := newComputeManager(log, cfg.ContainerRuntime)
	if err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, err)
	}
	if err := manager.Start(ctx); err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("starting compute container manager: %w", err))
	}
	defer func() {
		if stopErr := manager.Stop(); stopErr != nil {
			log.WithError(stopErr).Warn("Failed to stop compute container manager")
		}
	}()

	plan := planCampaign(
		workload, cfg.Seed, cfg.Sessions, cfg.PilotRepetitions, cfg.WarmupRepetitions, cfg.Repetitions,
	)
	if err := writeRequests(runDir, plan); err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, err)
	}
	limits, err := runner.ContainerResourceLimits(cfg.ResourceLimits)
	if err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, err)
	}
	phasePlan := phasePlanFacts(plan, cfg)
	images, imageErr := resolveCampaignImages(ctx, manager, cfg)
	if imageErr != nil {
		manifest := buildComputeManifest(
			ctx, cfg, archivedWorkload, workloadSHA, map[string]string{}, workloadSelection(workload), phasePlan, limits,
		)
		manifest.Software["image_resolution_error"] = imageErr.Error()
		if err := writeComputeJSON(filepath.Join(runDir, "manifest.json"), &manifest); err != nil {
			return runDir, fmt.Errorf("writing campaign manifest after image resolution failure: %w", err)
		}

		return finishUnstartedCampaign(ctx, log, runDir, &summary, flattenRequests(plan), boundary, cfg.Analyzer != nil, imageErr)
	}
	if generatedImage, ok := generatedWorkloadImage(workloadPath); ok {
		images["generator"] = generatedImage
	}
	campaign.WorkerImage = images["worker"]
	if campaign.Analyzer != nil {
		campaign.Analyzer.Image = images["analyzer"]
	}
	summary.Instance.Image = images["worker"]
	if err := writeComputeJSON(filepath.Join(runDir, "campaign.json"), &campaign); err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("recording resolved campaign images: %w", err))
	}
	manifest := buildComputeManifest(
		ctx, cfg, archivedWorkload, workloadSHA, images, workloadSelection(workload), phasePlan, limits,
	)
	if err := writeComputeJSON(filepath.Join(runDir, "manifest.json"), &manifest); err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("writing campaign manifest: %w", err))
	}
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		return runDir, recordCampaignFailure(runDir, &summary, fmt.Errorf("parsing compute timeout: %w", err))
	}

	canonical := make(map[string]Result, len(flattenRequests(plan)))
	var runErrors []error
	for _, session := range plan {
		results, sessionErr := runComputeSession(ctx, manager, runDir, session, limits, timeout, images["worker"], boundary)
		for sampleID, result := range results {
			canonical[sampleID] = result
		}
		if sessionErr != nil {
			runErrors = append(runErrors, sessionErr)
		}
	}
	requests := flattenRequests(plan)
	canonical = reconcileCampaignResults(requests, canonical, boundary)
	canonical = enforceCampaignIntegrity(requests, canonical)
	if err := writeCanonicalResults(runDir, requests, canonical); err != nil {
		return runDir, err
	}
	if err := writeExclusions(runDir, requests, canonical); err != nil {
		return runDir, err
	}

	updateSummaryCounts(&summary, canonical)
	if len(runErrors) > 0 || summary.TestCounts.Failed > 0 {
		summary.Status = "failed"
		summary.TerminationReason = joinErrors(runErrors)
		if summary.TerminationReason == "" {
			summary.TerminationReason = "one or more requested samples failed terminal accounting or correctness"
		}
		if len(runErrors) == 0 {
			runErrors = append(runErrors, errors.New(summary.TerminationReason))
		}
	} else {
		summary.Status = "completed"
	}
	summary.TimestampEnd = time.Now().Unix()
	if err := writeCampaignSummary(runDir, &summary); err != nil {
		return runDir, err
	}

	if cfg.Analyzer != nil {
		if _, analysisErr := Analyze(ctx, log, runDir, ""); analysisErr != nil {
			runErrors = append(runErrors, fmt.Errorf("analyzing compute campaign: %w", analysisErr))
		}
	}
	if len(runErrors) > 0 {
		return runDir, errors.Join(runErrors...)
	}

	return runDir, nil
}

func finishUnstartedCampaign(ctx context.Context, log logrus.FieldLogger, runDir string, summary *campaignSummary, requests []Request, boundary string, analyze bool, cause error) (string, error) {
	results := reconcileCampaignResults(requests, make(map[string]Result, len(requests)), boundary)
	if err := writeCanonicalResults(runDir, requests, results); err != nil {
		return runDir, err
	}
	if err := writeExclusions(runDir, requests, results); err != nil {
		return runDir, err
	}
	updateSummaryCounts(summary, results)
	summary.Status = "failed"
	summary.TerminationReason = cause.Error()
	summary.TimestampEnd = time.Now().Unix()
	if err := writeCampaignSummary(runDir, summary); err != nil {
		return runDir, err
	}
	if !analyze {
		return runDir, cause
	}
	_, analysisErr := Analyze(ctx, log, runDir, "")

	return runDir, errors.Join(cause, analysisErr)
}

func recordCampaignFailure(runDir string, summary *campaignSummary, cause error) error {
	summary.Status = "failed"
	summary.TerminationReason = cause.Error()
	summary.TimestampEnd = time.Now().Unix()
	if err := writeCampaignSummary(runDir, summary); err != nil {
		return errors.Join(cause, fmt.Errorf("recording failed compute campaign summary: %w", err))
	}

	return cause
}

func errorText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
func generateComputeWorkload(ctx context.Context, log logrus.FieldLogger, cfg *config.ComputeConfig) (string, error) {
	timeout, err := cfg.TimeoutDuration()
	if err != nil {
		return "", fmt.Errorf("parsing compute generator timeout: %w", err)
	}
	generatorCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	manager, err := newComputeManager(log, cfg.ContainerRuntime)
	if err != nil {
		return "", err
	}
	if err := manager.Start(generatorCtx); err != nil {
		return "", fmt.Errorf("starting generator container manager: %w", err)
	}
	defer func() {
		if stopErr := manager.Stop(); stopErr != nil {
			log.WithError(stopErr).Warn("Failed to stop generator container manager")
		}
	}()
	imageReference, err := resolveComputeImage(generatorCtx, manager, cfg.Generator.Image)
	if err != nil {
		return "", fmt.Errorf("resolving compute generator image: %w", err)
	}
	resultsDir, err := filepath.Abs(cfg.ResultsDir)
	if err != nil {
		return "", fmt.Errorf("resolving workload results directory: %w", err)
	}
	buildDir := filepath.Join(resultsDir, "workloads", "compute-build-"+uuid.NewString())
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return "", fmt.Errorf("creating workload build directory: %w", err)
	}
	outputPath := filepath.Join(buildDir, "workload.json")
	args := generatorArgs(cfg, imageReference)
	spec := &docker.ContainerSpec{
		Name:    "benchmarkoor-compute-generator-" + uuid.NewString(),
		Image:   imageReference,
		Command: args,
		Mounts:  []docker.Mount{{Source: buildDir, Target: "/out", Type: "bind"}},
		Labels:  map[string]string{"benchmarkoor.component": "compute-generator"},
	}
	if _, err := runComputeContainer(generatorCtx, manager, spec, filepath.Join(buildDir, "container.log")); err != nil {
		return "", err
	}
	if _, _, err := loadComputeWorkload(outputPath); err != nil {
		return "", fmt.Errorf("validating generated workload: %w", err)
	}
	if bytes, err := os.ReadFile(outputPath); err == nil {
		if err := writeComputeJSON(filepath.Join(buildDir, "build.json"), map[string]any{
			"image": cfg.Generator.Image, "image_reference": imageReference, "command": args, "workload_sha256": sha256Bytes(bytes),
		}); err != nil {
			return "", err
		}
	}

	return outputPath, nil
}

func generatorArgs(cfg *config.ComputeConfig, revision string) []string {
	generator := cfg.Generator
	args := []string{
		"--fork", SupportedFork,
		"--benchmark-workload-export=" + filepath.ToSlash(filepath.Join("/out", "workload.json")),
		"--benchmark-workload-seed=" + strconv.FormatInt(cfg.Seed, 10),
		"--benchmark-workload-revision=" + revision,
		"--benchmark-workload-tx-gas-cap=" + strconv.FormatUint(generatorTxGasCap(generator), 10),
		"--output=/out/fixtures", "--clean", "--no-html", "--skip-index", "-q",
	}
	if len(generator.FixedOpcodeCount) > 0 {
		counts := make([]string, len(generator.FixedOpcodeCount))
		for index, count := range generator.FixedOpcodeCount {
			counts[index] = strconv.FormatFloat(count, 'f', -1, 64)
		}
		args = append(args, "--fixed-opcode-count="+strings.Join(counts, ","))
	}
	if len(generator.Families) > 0 {
		args = append(args, "--benchmark-workload-families="+strings.Join(generator.Families, ","))
	}
	if generator.Filter != "" {
		args = append(args, "-k", generator.Filter)
	}
	if generator.Marker != "" {
		args = append(args, "-m", generator.Marker)
	}
	args = append(args, generator.Tests...)

	return args
}

func generatorTxGasCap(generator *config.ComputeGeneratorConfig) uint64 {
	if generator.TxGasCap == 0 {
		return defaultGeneratorTxGasCap
	}

	return generator.TxGasCap
}

func loadComputeWorkload(path string) (*Workload, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading workload: %w", err)
	}
	var workload Workload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&workload); err != nil {
		return nil, nil, fmt.Errorf("parsing workload: %w", err)
	}
	if len(bytes.TrimSpace(data[decoder.InputOffset():])) != 0 {
		return nil, nil, fmt.Errorf("parsing workload: unexpected trailing data")
	}
	if err := workload.Validate(); err != nil {
		return nil, nil, err
	}

	return &workload, data, nil
}

func archiveAnalysisConfig(runDir, configuredPath string) (string, error) {
	data, err := os.ReadFile(configuredPath)
	if err != nil {
		return "", fmt.Errorf("reading compute analysis configuration: %w", err)
	}
	path := filepath.Join(runDir, "analysis-config.yaml")
	if err := writeImmutableBytes(path, data); err != nil {
		return "", fmt.Errorf("archiving analysis configuration: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving archived analysis configuration: %w", err)
	}

	return absolute, nil
}

func resolveCampaignImages(ctx context.Context, manager docker.ContainerManager, cfg *config.ComputeConfig) (map[string]string, error) {
	requested := map[string]string{"worker": cfg.WorkerImage}
	if cfg.Analyzer != nil {
		requested["analyzer"] = cfg.Analyzer.Image
	}
	if cfg.Generator != nil {
		requested["generator"] = cfg.Generator.Image
	}
	images := make(map[string]string, len(requested))
	for name, image := range requested {
		reference, err := resolveComputeImage(ctx, manager, image)
		if err != nil {
			return nil, fmt.Errorf("resolving %s image: %w", name, err)
		}
		images[name] = reference
	}
	return images, nil
}

func generatedWorkloadImage(workloadPath string) (string, bool) {
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(workloadPath), "build.json"))
	if err != nil {
		return "", false
	}
	var build struct {
		ImageReference string `json:"image_reference"`
	}
	if err := json.Unmarshal(contents, &build); err != nil || build.ImageReference == "" {
		return "", false
	}

	return build.ImageReference, true
}

func planCampaign(workload *Workload, seed int64, sessions, pilot, warmup, repetitions int) []plannedSession {
	ready := make([]WorkloadCase, 0, len(workload.Cases))
	unsupported := make([]WorkloadCase, 0)
	for _, workloadCase := range workload.Cases {
		if workloadCase.Status == CaseStatusReady {
			ready = append(ready, workloadCase)
		} else {
			unsupported = append(unsupported, workloadCase)
		}
	}

	plan := make([]plannedSession, 0, 1+sessions*2)
	var nextSample uint64
	var orderingIndex int64
	newRequest := func(sessionID, mode string) Request {
		return Request{
			SchemaVersion: SchemaVersion,
			WorkloadPath:  filepath.ToSlash(filepath.Join(computeMountPath, "workload.json")),
			SessionID:     sessionID,
			Mode:          mode,
		}
	}
	appendSamples := func(request *Request, phase string, cases []WorkloadCase, phaseRepetitions int) {
		if phaseRepetitions == 0 {
			return
		}
		ordered := append([]WorkloadCase(nil), cases...)
		random := rand.New(rand.NewPCG(
			uint64(seed+orderingIndex*7919),
			uint64(seed)^0x9e3779b97f4a7c15,
		))
		orderingIndex++
		random.Shuffle(len(ordered), func(left, right int) {
			ordered[left], ordered[right] = ordered[right], ordered[left]
		})
		for repetition := range phaseRepetitions {
			for _, workloadCase := range ordered {
				nextSample++
				request.Samples = append(request.Samples, RequestSample{
					SampleID:   fmt.Sprintf("sample-%06d", nextSample),
					CaseID:     workloadCase.ID,
					Repetition: uint64(repetition),
					Phase:      phase,
				})
			}
		}
	}
	appendRequest := func(request Request) {
		if len(request.Samples) > 0 {
			plan = append(plan, plannedSession{Request: request})
		}
	}

	diagnostic := newRequest(fmt.Sprintf("%s-%02d", PhaseDiagnostic, 0), ModeDiagnostic)
	appendSamples(&diagnostic, PhaseDiagnostic, append(ready, unsupported...), 1)
	appendRequest(diagnostic)

	for sessionIndex := range sessions {
		pilotRequest := newRequest(fmt.Sprintf("%s-%02d", PhasePilot, sessionIndex), ModePerformance)
		appendSamples(&pilotRequest, PhasePilot, ready, pilot)
		appendRequest(pilotRequest)
	}
	for sessionIndex := range sessions {
		performanceRequest := newRequest(
			fmt.Sprintf("%s-%02d", PhaseQualification, sessionIndex),
			ModePerformance,
		)
		appendSamples(&performanceRequest, PhaseWarmup, ready, warmup)
		appendSamples(&performanceRequest, PhaseQualification, ready, repetitions)
		appendRequest(performanceRequest)
	}

	return plan
}

func writeRequests(runDir string, plan []plannedSession) error {
	requestedPath := filepath.Join(runDir, "requested-samples.jsonl")
	requested, err := os.OpenFile(requestedPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating requested samples ledger: %w", err)
	}
	defer requested.Close()
	for index := range plan {
		session := &plan[index]
		session.Path = filepath.Join(runDir, "sessions", session.Request.SessionID)
		if err := os.MkdirAll(session.Path, 0o755); err != nil {
			return fmt.Errorf("creating session directory: %w", err)
		}
		if err := session.Request.Validate(); err != nil {
			return fmt.Errorf("validating planned session %q: %w", session.Request.SessionID, err)
		}
		if err := writeComputeJSON(filepath.Join(session.Path, "request.json"), &session.Request); err != nil {
			return fmt.Errorf("writing request for session %q: %w", session.Request.SessionID, err)
		}
		for _, sample := range session.Request.Samples {
			if err := writeComputeJSONLine(requested, requestLedgerRecord(session.Request, sample)); err != nil {
				return err
			}
		}
	}

	return nil
}

func requestLedgerRecord(request Request, sample RequestSample) map[string]any {
	return map[string]any{"schema_version": SchemaVersion, "session_id": request.SessionID, "mode": request.Mode, "sample_id": sample.SampleID, "case_id": sample.CaseID, "repetition": sample.Repetition, "phase": sample.Phase}
}

func runComputeSession(ctx context.Context, manager docker.ContainerManager, runDir string, session plannedSession, limits *docker.ResourceLimits, timeout time.Duration, workerImage, boundary string) (map[string]Result, error) {
	sessionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	rawPath := filepath.Join(session.Path, "samples.jsonl")
	workerRequest := filepath.ToSlash(filepath.Join(computeMountPath, "sessions", session.Request.SessionID, "request.json"))
	workerOutput := filepath.ToSlash(filepath.Join(computeMountPath, "sessions", session.Request.SessionID, "samples.jsonl"))
	spec := &docker.ContainerSpec{
		Name:           "benchmarkoor-compute-worker-" + uuid.NewString(),
		Image:          workerImage,
		Command:        []string{"--request", workerRequest, "--output", workerOutput},
		Mounts:         []docker.Mount{{Source: runDir, Target: computeMountPath, Type: "bind"}},
		ResourceLimits: limits,
		Labels:         map[string]string{"benchmarkoor.component": "compute-worker", "benchmarkoor.session": session.Request.SessionID},
	}
	exit, runErr := runComputeContainer(sessionCtx, manager, spec, filepath.Join(session.Path, computeWorkerLogName))
	var exitCode *int64
	var oomKilled *bool
	if exit != nil {
		exitCode = &exit.ExitCode
		oomKilled = &exit.OOMKilled
	}
	if err := writeComputeJSON(filepath.Join(session.Path, "exit.json"), map[string]any{
		"exit_code":  exitCode,
		"oom_killed": oomKilled,
		"observed":   exit != nil,
		"error":      errorText(runErr),
	}); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("writing worker exit facts: %w", err))
	}
	results, parseErr := parseSessionResults(rawPath, session.Request, boundary)
	if runErr != nil {
		parseErr = errors.Join(parseErr, fmt.Errorf("worker session %q: %w", session.Request.SessionID, runErr))
	}
	if parseErr != nil {
		return failedSessionResults(session.Request, boundary, parseErr.Error()), parseErr
	}

	return results, nil
}

// parseSessionResults decodes one worker session's JSONL output. Every
// terminal record must validate and carry exactly the campaign engine's
// execution boundary; a foreign-boundary row is an accounting error.
func parseSessionResults(path string, request Request, boundary string) (map[string]Result, error) {
	results := make(map[string]Result, len(request.Samples))
	file, err := os.Open(path)
	if err != nil {
		return results, fmt.Errorf("opening worker output for session %q: %w", request.SessionID, err)
	}
	defer file.Close()
	expected := make(map[string]RequestSample, len(request.Samples))
	for _, sample := range request.Samples {
		expected[sample.SampleID] = sample
	}
	var parseErrors []error
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var result Result
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("malformed worker result line %d: %w", line, err))
			continue
		}
		sample, known := expected[result.SampleID]
		if !known || result.SessionID != request.SessionID || result.CaseID != sample.CaseID || result.Repetition != sample.Repetition || result.Phase != sample.Phase {
			parseErrors = append(parseErrors, fmt.Errorf("worker result line %d has an unexpected sample identity", line))
			continue
		}
		if err := result.Validate(); err != nil {
			parseErrors = append(parseErrors, fmt.Errorf("invalid worker result for sample %q: %w", result.SampleID, err))
			continue
		}
		if result.ExecutionBoundary != boundary {
			parseErrors = append(parseErrors, fmt.Errorf(
				"invalid worker result for sample %q: execution_boundary %q is not the campaign boundary %q",
				result.SampleID, result.ExecutionBoundary, boundary,
			))
			continue
		}
		if _, duplicate := results[result.SampleID]; duplicate {
			results[result.SampleID] = controllerFailure(boundary, request, sample, "reconciliation", "worker emitted duplicate terminal results for this sample")
			parseErrors = append(parseErrors, fmt.Errorf("duplicate worker result for sample %q", result.SampleID))
			continue
		}
		results[result.SampleID] = result
	}
	if err := scanner.Err(); err != nil {
		parseErrors = append(parseErrors, fmt.Errorf("reading worker output: %w", err))
	}
	if len(results) != len(request.Samples) {
		parseErrors = append(parseErrors, fmt.Errorf(
			"worker output for session %q contains %d terminal records for %d requested samples",
			request.SessionID, len(results), len(request.Samples),
		))
	}

	return results, errors.Join(parseErrors...)
}

func failedSessionResults(request Request, boundary string, message string) map[string]Result {
	results := make(map[string]Result, len(request.Samples))
	for _, sample := range request.Samples {
		results[sample.SampleID] = controllerFailure(boundary, request, sample, "worker_session", message)
	}

	return results
}

func reconcileCampaignResults(requests []Request, results map[string]Result, boundary string) map[string]Result {
	for _, request := range requests {
		for _, sample := range request.Samples {
			if _, exists := results[sample.SampleID]; !exists {
				results[sample.SampleID] = controllerFailure(boundary, request, sample, "reconciliation", "worker did not emit a terminal result")
			}
		}
	}

	return results
}

func enforceCampaignIntegrity(requests []Request, results map[string]Result) map[string]Result {
	diagnostics := make(map[string]Result)
	baselineByCase := make(map[string]string)
	commitmentByCase := make(map[string]string)
	for _, request := range requests {
		for _, sample := range request.Samples {
			result := results[sample.SampleID]
			if sample.Phase != PhaseDiagnostic || result.Status != ResultStatusExecuted || !result.CorrectnessPassed || result.PreparedHash == nil || result.BaselineHash == nil || result.CommitmentHash == nil {
				continue
			}
			diagnostics[sample.CaseID] = result
			baselineByCase[sample.CaseID] = *result.BaselineHash
			commitmentByCase[sample.CaseID] = *result.CommitmentHash
		}
	}
	for _, request := range requests {
		for _, sample := range request.Samples {
			result := results[sample.SampleID]
			if sample.Phase == PhaseDiagnostic || result.Status != ResultStatusExecuted || !result.CorrectnessPassed {
				continue
			}
			diagnostic, ok := diagnostics[sample.CaseID]
			if !ok || result.PreparedHash == nil || diagnostic.PreparedHash == nil || *result.PreparedHash != *diagnostic.PreparedHash {
				results[sample.SampleID] = integrityFailure(result, "diagnostic linkage is missing or has a different prepared_hash")
				continue
			}
			if result.BaselineHash == nil || *result.BaselineHash != baselineByCase[sample.CaseID] {
				results[sample.SampleID] = integrityFailure(result, "baseline identity differs from diagnostic execution")
				continue
			}
			if result.CommitmentHash == nil || *result.CommitmentHash != commitmentByCase[sample.CaseID] {
				results[sample.SampleID] = integrityFailure(result, "commitment identity differs from the restored baseline execution")
			}
		}
	}

	return results
}

func controllerFailure(boundary string, request Request, sample RequestSample, stage, message string) Result {
	return Result{SchemaVersion: SchemaVersion, SessionID: request.SessionID, SampleID: sample.SampleID, CaseID: sample.CaseID, Repetition: sample.Repetition, Phase: sample.Phase, Status: ResultStatusFailed, ExecutionBoundary: boundary, Error: &ResultError{Stage: stage, Message: message}}
}

func integrityFailure(result Result, message string) Result {
	result.Status = ResultStatusFailed
	result.CorrectnessPassed = false
	result.TargetCount = nil
	result.OpcodeCounts = nil
	result.Error = &ResultError{Stage: "integrity", Message: message}

	return result
}

func writeCanonicalResults(runDir string, requests []Request, results map[string]Result) error {
	path := filepath.Join(runDir, "samples.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating canonical samples ledger: %w", err)
	}
	defer file.Close()
	for _, request := range requests {
		for _, sample := range request.Samples {
			result := results[sample.SampleID]
			if err := writeComputeJSONLine(file, result); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeExclusions(runDir string, requests []Request, results map[string]Result) error {
	path := filepath.Join(runDir, "exclusions.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("creating exclusions ledger: %w", err)
	}
	defer file.Close()
	for _, request := range requests {
		for _, sample := range request.Samples {
			result := results[sample.SampleID]
			if result.Status == ResultStatusExecuted && result.CorrectnessPassed && sample.Phase == PhaseQualification {
				continue
			}
			reason := "not an eligible qualification execution"
			if result.Error != nil {
				reason = result.Error.Stage + ": " + result.Error.Message
			}
			if err := writeComputeJSONLine(file, map[string]any{"sample_id": sample.SampleID, "case_id": sample.CaseID, "session_id": request.SessionID, "phase": sample.Phase, "status": result.Status, "reason": reason}); err != nil {
				return err
			}
		}
	}

	return nil
}

func flattenRequests(plan []plannedSession) []Request {
	requests := make([]Request, len(plan))
	for index := range plan {
		requests[index] = plan[index].Request
	}

	return requests
}

func newCampaignSummary(cfg *config.ComputeConfig, workloadSHA string) campaignSummary {
	var summary campaignSummary
	summary.Timestamp = time.Now().Unix()
	summary.SuiteHash = workloadSHA
	summary.Status = "running"
	summary.Instance.ID = cfg.ID
	summary.Instance.Client = cfg.Engine
	summary.Instance.Image = cfg.WorkerImage
	summary.Metadata.Labels = map[string]string{"campaign": cfg.ID, "mode": "compute"}
	summary.Compute.SchemaVersion = computeManifestSchemaVersion
	summary.Compute.ManifestPath = "manifest.json"
	summary.Compute.SamplesPath = "samples.jsonl"
	summary.Compute.Analysis.Status = computeAnalysisPending

	return summary
}

func writeCampaignSummary(runDir string, summary *campaignSummary) error {
	if err := writeComputeJSON(filepath.Join(runDir, "config.json"), summary); err != nil {
		return fmt.Errorf("writing compute run summary: %w", err)
	}

	resultsDir := filepath.Dir(filepath.Dir(runDir))
	index, err := executor.GenerateIndex(resultsDir)
	if err != nil {
		return fmt.Errorf("generating compute run index: %w", err)
	}
	if err := executor.WriteIndex(resultsDir, index, nil); err != nil {
		return fmt.Errorf("publishing compute run index: %w", err)
	}

	return nil
}

func updateSummaryCounts(summary *campaignSummary, results map[string]Result) {
	summary.TestCounts.Total = len(results)
	for _, result := range results {
		if result.Status == ResultStatusExecuted && result.CorrectnessPassed {
			summary.TestCounts.Passed++
		} else if result.Status == ResultStatusFailed {
			summary.TestCounts.Failed++
		}
	}
}

func workloadSelection(workload *Workload) map[string]any {
	cases := make([]map[string]any, 0, len(workload.Cases))
	for _, workloadCase := range workload.Cases {
		cases = append(cases, map[string]any{"id": workloadCase.ID, "family": workloadCase.Family, "target_operation": workloadCase.TargetOperation, "status": workloadCase.Status, "parameters": workloadCase.Parameters})
	}
	sort.Slice(cases, func(left, right int) bool { return cases[left]["id"].(string) < cases[right]["id"].(string) })
	return map[string]any{"fork": workload.Fork, "generator": workload.Generator, "cases": cases}
}

func phasePlanFacts(plan []plannedSession, cfg *config.ComputeConfig) map[string]any {
	sessions := make([]map[string]any, 0, len(plan))
	for _, session := range plan {
		facts := map[string]any{
			"session_id":   session.Request.SessionID,
			"mode":         session.Request.Mode,
			"sample_count": len(session.Request.Samples),
			"phase":        phaseForSession(session.Request),
		}
		if phases := phasesForSession(session.Request); len(phases) > 1 {
			facts["phases"] = phases
		}
		sessions = append(sessions, facts)
	}
	return map[string]any{
		"sessions":                     sessions,
		"pilot_repetitions":            cfg.PilotRepetitions,
		"warmup_repetitions":           cfg.WarmupRepetitions,
		"qualification_repetitions":    cfg.Repetitions,
		"independent_worker_processes": true,
	}
}

func phaseForSession(request Request) string {
	phases := phasesForSession(request)
	if len(phases) == 0 {
		return ""
	}
	if len(phases) > 1 {
		return "mixed"
	}
	return phases[0]
}

func phasesForSession(request Request) []string {
	phases := make([]string, 0, len(request.Samples))
	seen := make(map[string]struct{}, len(request.Samples))
	for _, sample := range request.Samples {
		if _, ok := seen[sample.Phase]; !ok {
			seen[sample.Phase] = struct{}{}
			phases = append(phases, sample.Phase)
		}
	}
	return phases
}

func writeImmutableBytes(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("immutable artifact already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking immutable artifact %q: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing immutable artifact %q: %w", path, err)
	}
	return nil
}

func joinErrors(errs []error) string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	return strings.Join(messages, "; ")
}
