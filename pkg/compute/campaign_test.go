package compute

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethpandaops/benchmarkoor/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSessionResultsRejectsPartialOutputAndReconcilesMissingSample(t *testing.T) {
	request := *baseRequest()
	request.Samples = append(request.Samples, RequestSample{
		SampleID: "s-0002",
		CaseID:   addCaseID,
		Phase:    PhaseDiagnostic,
	})
	failed := controllerFailure(request, request.Samples[0], "worker", "fixture failure")
	data, err := json.Marshal(failed)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))

	results, err := parseSessionResults(path, request)
	require.Error(t, err)
	reconciled := reconcileCampaignResults([]Request{request}, results)
	require.Len(t, reconciled, 2)
	assert.Equal(t, ResultStatusFailed, reconciled["s-0001"].Status)
	assert.Equal(t, ResultStatusFailed, reconciled["s-0002"].Status)
	require.NotNil(t, reconciled["s-0002"].Error)
	missing := reconciled["s-0002"]
	require.NoError(t, missing.Validate())
}

func TestParseSessionResultsRejectsMalformedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("{not-json}\n"), 0o644))

	results, err := parseSessionResults(path, *baseRequest())
	require.Error(t, err)
	assert.Empty(t, results)
}

func TestParseSessionResultsRejectsDuplicateOutput(t *testing.T) {
	request := *baseRequest()
	failed := controllerFailure(request, request.Samples[0], "worker", "fixture failure")
	data, err := json.Marshal(failed)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	require.NoError(t, os.WriteFile(path, append(append(data, '\n'), append(data, '\n')...), 0o644))

	_, err = parseSessionResults(path, request)
	require.Error(t, err)
}

func TestParseSessionResultsRetainsTerminalWorkerFailure(t *testing.T) {
	request := *baseRequest()
	failed := controllerFailure(request, request.Samples[0], "worker", "fixture failure")
	data, err := json.Marshal(failed)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o644))

	results, err := parseSessionResults(path, request)
	require.NoError(t, err)
	require.Contains(t, results, request.Samples[0].SampleID)
	assert.Equal(t, ResultStatusFailed, results[request.Samples[0].SampleID].Status)
}

func TestPlanCampaignWarmsQualificationWorkersDeterministically(t *testing.T) {
	workload := baseWorkload()
	workload.Cases = append(workload.Cases, WorkloadCase{
		ID:              modCaseID,
		Family:          "arithmetic",
		TargetOperation: "MOD",
		Status:          CaseStatusUnsupported,
		Reason:          "fixture is intentionally unsupported",
	})

	first := planCampaign(workload, 20260921, 2, 1, 1, 2)
	second := planCampaign(workload, 20260921, 2, 1, 1, 2)
	require.Equal(t, first, second)
	require.Len(t, first, 5)

	sampleIDs := make(map[string]struct{})
	for _, session := range first {
		require.NotEmpty(t, session.Request.Samples)
		require.NoError(t, session.Request.Validate())
		for _, sample := range session.Request.Samples {
			_, duplicate := sampleIDs[sample.SampleID]
			assert.False(t, duplicate)
			sampleIDs[sample.SampleID] = struct{}{}
			if sample.Phase == PhaseDiagnostic {
				assert.Equal(t, ModeDiagnostic, session.Request.Mode)
			} else {
				assert.Equal(t, ModePerformance, session.Request.Mode)
			}
		}
	}

	for _, session := range first[1:3] {
		assert.Equal(t, PhasePilot, phaseForSession(session.Request))
		for _, sample := range session.Request.Samples {
			assert.Equal(t, PhasePilot, sample.Phase)
		}
	}
	for _, session := range first[3:] {
		assert.Equal(t, "mixed", phaseForSession(session.Request))
		require.Len(t, session.Request.Samples, 3)
		assert.Equal(t, PhaseWarmup, session.Request.Samples[0].Phase)
		assert.Equal(t, PhaseQualification, session.Request.Samples[1].Phase)
		assert.Equal(t, PhaseQualification, session.Request.Samples[2].Phase)
	}

	facts := phasePlanFacts(first, &config.ComputeConfig{
		PilotRepetitions:  1,
		WarmupRepetitions: 1,
		Repetitions:       2,
	})
	sessions := facts["sessions"].([]map[string]any)
	for _, session := range sessions[3:] {
		assert.Equal(t, "mixed", session["phase"])
		assert.Equal(t, []string{PhaseWarmup, PhaseQualification}, session["phases"])
	}
}

func TestComputeManifestSeparatesComparisonFactorsFromRunProvenance(t *testing.T) {
	cfg := &config.ComputeConfig{ContainerRuntime: "docker", Seed: 20260921}
	selection := map[string]any{"fork": SupportedFork, "cases": []string{addCaseID}}
	first := buildComputeManifest(
		t.Context(),
		cfg,
		"/runs/first/workload.json",
		"workload-sha256",
		map[string]string{"worker": "sha256:worker"},
		selection,
		map[string]any{},
		nil,
	)
	second := buildComputeManifest(
		t.Context(),
		cfg,
		"/runs/second/workload.json",
		"workload-sha256",
		map[string]string{"worker": "sha256:worker"},
		selection,
		map[string]any{},
		nil,
	)

	assert.Equal(t, first.ComparisonFactors, second.ComparisonFactors)
	assert.Equal(t, "/runs/first/workload.json", first.Policy["workload_artifact_path"])
	assert.NotContains(t, first.ComparisonFactors["workload"], "path")
	assert.NotContains(t, first.Hardware, "cpu_mhz")
	assert.NotContains(t, first.ComparisonFactors["hardware"], "cpu_mhz")
	assert.True(
		t,
		first.HostProvenance["cpu_mhz"] != nil ||
			first.HostProvenance["cpu_mhz_unavailable"] != nil,
	)

	workloadFactors := first.ComparisonFactors["workload"].(map[string]any)
	assert.Equal(t, "workload-sha256", workloadFactors["sha256"])
	assert.Equal(t, selection, workloadFactors["resolved_selection"])
	assert.Equal(
		t,
		computeFixedLimits(cfg),
		workloadFactors["fixed_limits"],
	)
	gasSchedule := first.ComparisonFactors["gas_schedule"].(map[string]any)
	identity := gasSchedule["active_schedule_identity"].(map[string]string)
	assert.Equal(t, "unavailable", identity["status"])
	assert.NotEmpty(t, identity["reason"])
}
