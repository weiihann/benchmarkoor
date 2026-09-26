package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeConfigValidate(t *testing.T) {
	t.Parallel()

	analysisConfig := writeComputeAnalysisConfig(t)
	valid := func() *ComputeConfig {
		return &ComputeConfig{
			ID:               "osaka-add-keccak-local",
			ResultsDir:       t.TempDir(),
			ContainerRuntime: "docker",
			Engine:           ComputeEngineNewL1,
			WorkerImage:      "benchmarkoor-compute-worker-newl1:local",
			Generator: &ComputeGeneratorConfig{
				Image:            "benchmarkoor-compute-generator:local",
				Marker:           "repricing",
				FixedOpcodeCount: []float64{0, 1, 2},
				TxGasCap:         DefaultComputeTxGasCap,
			},
			Analyzer: &ComputeAnalyzerConfig{
				Image:  "benchmarkoor-compute-analyzer:local",
				Config: analysisConfig,
			},
			Sessions:          2,
			PilotRepetitions:  1,
			WarmupRepetitions: 1,
			Repetitions:       1,
			Timeout:           "5m",
		}
	}

	t.Run("zero work control is valid with a positive measurement point", func(t *testing.T) {
		cfg := valid()
		require.NoError(t, cfg.Validate())
		require.NoError(t, (&Config{Compute: cfg}).Validate())
	})

	t.Run("cpu frequency settings are rejected because compute sessions never apply them", func(t *testing.T) {
		turbo := false
		for name, limits := range map[string]*ResourceLimits{
			"cpu_freq":          {CPUFreq: "3600MHz"},
			"cpu_turboboost":    {CPUTurboBoost: &turbo},
			"cpu_freq_governor": {CPUGovernor: "performance"},
		} {
			cfg := valid()
			cfg.ResourceLimits = limits
			assert.ErrorContains(t, cfg.Validate(), "not applied to compute campaigns", name)
		}
	})

	t.Run("campaign analysis requires an explicit frozen qualification policy", func(t *testing.T) {
		cfg := valid()
		path := filepath.Join(t.TempDir(), "analysis.yaml")
		require.NoError(t, os.WriteFile(path, []byte("clients: [newl1]\ncampaign: {}\n"), 0o600))
		cfg.Analyzer.Config = path

		assert.Error(t, cfg.Validate())
	})

	t.Run("campaign analysis rejects a disabled qualification gate", func(t *testing.T) {
		cfg := valid()
		path := filepath.Join(t.TempDir(), "analysis.yaml")
		require.NoError(t, os.WriteFile(path, []byte(`
clients: [newl1]
qualification:
  confidence_level: 0.95
  max_relative_uncertainty: 0.2
  max_holdout_error: null
  min_sessions: 2
campaign: {}
`), 0o600))
		cfg.Analyzer.Config = path

		assert.Error(t, cfg.Validate())
	})

	t.Run("campaign analysis rejects eligibility policy bypasses", func(t *testing.T) {
		for _, test := range []struct {
			name          string
			qualification string
			content       string
		}{
			{
				name: "warmup mixed with qualification",
				content: `
campaign:
  eligible_phases: [qualification, warmup]
`,
			},
			{
				name: "failed status",
				content: `
campaign:
  eligible_statuses: [failed]
`,
			},
			{
				name: "correctness disabled",
				content: `
campaign:
  require_correctness_passed: false
`,
			},
			{
				name:          "unqualified prices enabled",
				qualification: "  block_unqualified: false\n",
				content: `
campaign: {}
`,
			},
			{
				name: "eligibility columns hidden as legacy data",
				content: `
campaign:
  phase_column: missing_phase
  status_column: missing_status
`,
			},
			{
				name: "samples relabeled as independent sessions",
				content: `
campaign:
  session_column: sample_id
`,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				cfg := valid()
				path := filepath.Join(t.TempDir(), "analysis.yaml")
				require.NoError(t, os.WriteFile(path, []byte(`
clients: [newl1]
qualification:
  confidence_level: 0.95
  max_relative_uncertainty: 0.2
  max_holdout_error: 0.1
  min_sessions: 2
`+test.qualification+test.content), 0o600))
				cfg.Analyzer.Config = path

				assert.Error(t, cfg.Validate())
				assert.Error(t, (&Config{Compute: cfg}).Validate())
			})
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*ComputeConfig)
	}{
		{
			name: "all control counts are rejected",
			mutate: func(cfg *ComputeConfig) {
				cfg.Generator.FixedOpcodeCount = []float64{0}
			},
		},
		{
			name: "workload and generator are mutually exclusive",
			mutate: func(cfg *ComputeConfig) {
				cfg.Workload = analysisConfig
			},
		},
		{
			name: "nonfinite count is rejected",
			mutate: func(cfg *ComputeConfig) {
				cfg.Generator.FixedOpcodeCount = []float64{1, math.Inf(1)}
			},
		},
		{
			name: "Osaka gas cap is enforced",
			mutate: func(cfg *ComputeConfig) {
				cfg.Generator.TxGasCap = maxComputeTxGasCap + 1
			},
		},
		{
			name: "resource limit requires a positive CPU count",
			mutate: func(cfg *ComputeConfig) {
				count := 0
				cfg.ResourceLimits = &ResourceLimits{CpusetCount: &count}
			},
		},
		{
			name: "independent session requirement is enforced",
			mutate: func(cfg *ComputeConfig) {
				cfg.Sessions = 1
			},
		},
		{
			name: "warmup phase requires repetitions",
			mutate: func(cfg *ComputeConfig) {
				cfg.WarmupRepetitions = 0
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid()
			test.mutate(cfg)
			assert.Error(t, cfg.Validate())
		})
	}
}

func TestComputeConfigLoadsCaptureOnlyCampaign(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	workload := filepath.Join(dir, "workload.json")
	require.NoError(t, os.WriteFile(workload, []byte("{}"), 0o600))
	path := filepath.Join(dir, "compute.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`compute:
  id: block-times
  engine: newl1
  workload: `+workload+`
  results_dir: `+dir+`
  container_runtime: docker
  worker_image: benchmarkoor-compute-worker-newl1:local
  seed: 1
  sessions: 2
  pilot_repetitions: 1
  warmup_repetitions: 1
  repetitions: 1
  timeout: 5m
`), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	// Env-bound analyzer keys must not materialise an empty analyzer, which
	// would fail validation instead of selecting a capture-only campaign.
	assert.Nil(t, cfg.Compute.Analyzer)
	require.NoError(t, cfg.ValidateCompute())
}

func writeComputeAnalysisConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "analysis.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
clients: [newl1]
qualification:
  confidence_level: 0.95
  max_relative_uncertainty: 0.2
  max_holdout_error: 0.1
  min_sessions: 2
campaign:
  eligible_phases: [qualification]
  eligible_statuses: [executed]
`), 0o600))

	return path
}
