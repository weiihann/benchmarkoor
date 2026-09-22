package compute

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExportGasfitInputsSnapshotsRawArtifactsWithoutMutation(t *testing.T) {
	runDir := t.TempDir()
	attemptDir := filepath.Join(runDir, "analysis", "attempt")
	require.NoError(t, os.MkdirAll(attemptDir, 0o755))

	workloadBytes, err := json.Marshal(baseWorkload())
	require.NoError(t, err)
	diagnostic := baseResult()
	performance := *diagnostic
	performance.SampleID = "s-qualification-0001"
	performance.Phase = PhaseQualification
	performance.Repetition = 1
	performance.TargetCount = nil
	performance.OpcodeCounts = nil
	performance.ExecutionDurationNS = u64(482_135_001)
	performanceBytes, err := json.Marshal(performance)
	require.NoError(t, err)
	diagnosticBytes, err := json.Marshal(diagnostic)
	require.NoError(t, err)

	manifestBytes := []byte(`{"schema_version":2,"boundary":"evm2_transaction_execution"}`)
	configBytes := []byte("clients:\n  - evm2\n")
	for path, contents := range map[string][]byte{
		filepath.Join(runDir, "workload.json"):        workloadBytes,
		filepath.Join(runDir, "samples.jsonl"):        append(append(diagnosticBytes, '\n'), append(performanceBytes, '\n')...),
		filepath.Join(runDir, "manifest.json"):        manifestBytes,
		filepath.Join(runDir, "analysis-config.yaml"): configBytes,
	} {
		require.NoError(t, os.WriteFile(path, contents, 0o644))
	}
	originalWorkload, err := os.ReadFile(filepath.Join(runDir, "workload.json"))
	require.NoError(t, err)
	originalSamples, err := os.ReadFile(filepath.Join(runDir, "samples.jsonl"))
	require.NoError(t, err)

	inputs, err := exportGasfitInputs(runDir, attemptDir, filepath.Join(runDir, "analysis-config.yaml"))
	require.NoError(t, err)
	assert.NotEmpty(t, inputs.Hashes["workload"])
	assert.NotEmpty(t, inputs.Hashes["samples"])
	assert.Equal(t, originalWorkload, mustReadFile(t, filepath.Join(runDir, "workload.json")))
	assert.Equal(t, originalSamples, mustReadFile(t, filepath.Join(runDir, "samples.jsonl")))
	assert.Equal(t, originalWorkload, mustReadFile(t, filepath.Join(attemptDir, "workload.json")))
	assert.Equal(t, originalSamples, mustReadFile(t, filepath.Join(attemptDir, "samples.jsonl")))

	csvFile, err := os.Open(inputs.RuntimesPath)
	require.NoError(t, err)
	defer csvFile.Close()
	rows, err := csv.NewReader(csvFile).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	header := csvHeaderIndex(rows[0])
	assert.Equal(t, addCaseID, rows[2][header["fixture_name"]])
	assert.Equal(t, "482.135001", rows[2][header["test_runtime_ms"]])
	assert.Equal(t, "1000", rows[2][header["param_requested_count"]])
	assert.NotContains(t, header, "workload_param_requested_count")
}

func TestTimingDiagnosticLinkageRejectsPreparedHashMismatch(t *testing.T) {
	workload := baseWorkload()
	diagnostic := baseResult()
	diagnostics, err := collectDiagnosticCounts(workload, []Result{*diagnostic})
	require.NoError(t, err)

	performance := *diagnostic
	performance.SampleID = "s-qualification-0002"
	performance.Phase = PhaseQualification
	performance.TargetCount = nil
	performance.OpcodeCounts = nil
	performance.ExecutionDurationNS = u64(1)
	performance.PreparedHash = strPtr("0a3c6e1f8b4d7c2a5e0f3b6d9c1a4e7f2b5d8c0a3e6f1b4d7c2a5e8f103b6d9c")

	err = checkTimingDiagnosticLinkage([]Result{performance}, diagnostics)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prepared_hash does not match")
}

func TestPrecompileOpcountUsesSemanticTargetCounter(t *testing.T) {
	precompile := &WorkloadCase{
		ID:              "tests/test_precompile.py::test_blake2f[count_1]",
		TargetOperation: "BLAKE2F",
		Parameters: map[string]any{
			"precompile_address": "0x0000000000000000000000000000000000000009",
		},
	}
	target := uint64(7)
	result := Result{
		SampleID:    "diagnostic-precompile",
		TargetCount: &target,
		OpcodeCounts: map[string]uint64{
			"PRECOMPILE_0x0000000000000000000000000000000000000009": 7,
			"STATICCALL": 11,
		},
	}
	require.NoError(t, validateTargetCount(precompile, result))

	path := filepath.Join(t.TempDir(), "opcounts.json")
	require.NoError(t, writeGasfitOpcounts(path, map[string]diagnosticCounts{
		precompile.ID: {counts: result.OpcodeCounts, targetCount: target},
	}))
	var exported map[string]map[string]uint64
	require.NoError(t, json.Unmarshal(mustReadFile(t, path), &exported))
	assert.Equal(t, uint64(7), exported[precompile.ID]["opcount"])
	assert.Equal(t, uint64(7), exported[precompile.ID]["PRECOMPILE_0x0000000000000000000000000000000000000009"])
	assert.Equal(t, uint64(11), exported[precompile.ID]["STATICCALL"])
}

func csvHeaderIndex(header []string) map[string]int {
	index := make(map[string]int, len(header))
	for i, name := range header {
		index[name] = i
	}
	return index
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	return contents
}
