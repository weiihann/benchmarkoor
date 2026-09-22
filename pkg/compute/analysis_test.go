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

func TestUpdateComputeAnalysisPreservesExistingSummaryFields(t *testing.T) {
	runDir := t.TempDir()
	original := []byte(`{
  "timestamp": 1,
  "status": "completed",
  "instance": {"id":"compute","client":"newl1","image":"worker@sha256:abc"},
  "test_counts": {"total":4,"passed":3,"failed":1},
  "metadata": {"labels":{"campaign":"prague"}},
  "compute": {
    "schema_version": 1,
    "manifest_path": "manifest.json",
    "samples_path": "samples.jsonl",
    "analysis": {"status":"pending","artifacts":[]}
  }
}`)
	require.NoError(t, os.WriteFile(filepath.Join(runDir, "config.json"), original, 0o644))

	status := analysisAttemptStatus{
		AttemptID: "attempt-1",
		Status:    "inconclusive",
		Artifacts: []analysisArtifact{{Name: "qualification.csv", Path: "analysis/attempt-1/reports/qualification.csv"}},
	}
	require.NoError(t, updateComputeAnalysis(runDir, status))

	var updated map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(mustReadFile(t, filepath.Join(runDir, "config.json")), &updated))
	assert.JSONEq(t, `{"total":4,"passed":3,"failed":1}`, string(updated["test_counts"]))
	assert.JSONEq(t, `{"labels":{"campaign":"prague"}}`, string(updated["metadata"]))
	var compute map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(updated["compute"], &compute))
	assert.JSONEq(t, `"manifest.json"`, string(compute["manifest_path"]))
	assert.JSONEq(t, `"samples.jsonl"`, string(compute["samples_path"]))
	assert.JSONEq(t, `{"status":"inconclusive","attempt_id":"attempt-1","artifacts":[{"name":"qualification.csv","path":"analysis/attempt-1/reports/qualification.csv"}]}`, string(compute["analysis"]))
}

func TestGasfitOutcomePreservesFailurePrecedence(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), "analysis_status.json")
	require.NoError(t, os.WriteFile(statusPath, []byte(`{
  "planned_models": [
    {"status":"inconclusive","adjusted_estimate_status":"inconclusive"},
    {"status":"failed","adjusted_estimate_status":"qualified"}
  ]
}`), 0o600))

	outcome, err := gasfitOutcome(statusPath)

	require.NoError(t, err)
	assert.Equal(t, "failed", outcome)
}

func TestGasfitOutcomeRequiresQualifiedAdjustedEstimate(t *testing.T) {
	statusPath := filepath.Join(t.TempDir(), "analysis_status.json")
	require.NoError(t, os.WriteFile(statusPath, []byte(`{
  "planned_models": [
    {"status":"qualified","adjusted_estimate_status":"inconclusive"}
  ]
}`), 0o600))

	outcome, err := gasfitOutcome(statusPath)

	require.NoError(t, err)
	assert.Equal(t, "inconclusive", outcome)
}

func TestAnalysisOverrideRejectsDisabledQualificationGate(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "permissive-analysis.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
clients: [newl1]
qualification:
  confidence_level: 0.95
  max_relative_uncertainty: null
  max_holdout_error: 0.1
  min_sessions: 2
campaign: {}
`), 0o600))

	err := config.ValidateComputeQualificationPolicy(configPath)

	assert.ErrorContains(t, err, "positive qualification.max_relative_uncertainty")
}
