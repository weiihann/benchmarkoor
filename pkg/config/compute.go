package config

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultComputeTxGasCap is the fixed native transaction allowance used when
	// a compute generator does not override it.
	DefaultComputeTxGasCap uint64 = 15_000_000

	// maxComputeTxGasCap is Osaka's EIP-7825 transaction gas limit.
	maxComputeTxGasCap uint64 = 1 << 24

	// ComputeEngineEvm2 measures the evm2 transaction executor.
	ComputeEngineEvm2 = "evm2"
	// ComputeEngineNewL1 measures the NewL1 production block executor.
	ComputeEngineNewL1 = "newl1"
)

// ComputeConfig configures an Osaka compute campaign for one execution
// engine.
type ComputeConfig struct {
	ID                string                  `yaml:"id" mapstructure:"id" json:"id"`
	Engine            string                  `yaml:"engine" mapstructure:"engine" json:"engine"`
	Workload          string                  `yaml:"workload,omitempty" mapstructure:"workload" json:"workload"`
	ResultsDir        string                  `yaml:"results_dir,omitempty" mapstructure:"results_dir" json:"results_dir"`
	ContainerRuntime  string                  `yaml:"container_runtime,omitempty" mapstructure:"container_runtime" json:"container_runtime"`
	WorkerImage       string                  `yaml:"worker_image" mapstructure:"worker_image" json:"worker_image"`
	Generator         *ComputeGeneratorConfig `yaml:"generator,omitempty" mapstructure:"generator" json:"generator,omitempty"`
	Analyzer          ComputeAnalyzerConfig   `yaml:"analyzer" mapstructure:"analyzer" json:"analyzer"`
	Seed              int64                   `yaml:"seed" mapstructure:"seed" json:"seed"`
	Sessions          int                     `yaml:"sessions" mapstructure:"sessions" json:"sessions"`
	PilotRepetitions  int                     `yaml:"pilot_repetitions" mapstructure:"pilot_repetitions" json:"pilot_repetitions"`
	WarmupRepetitions int                     `yaml:"warmup_repetitions" mapstructure:"warmup_repetitions" json:"warmup_repetitions"`
	Repetitions       int                     `yaml:"repetitions" mapstructure:"repetitions" json:"repetitions"`
	Timeout           string                  `yaml:"timeout" mapstructure:"timeout" json:"timeout"`
	ResourceLimits    *ResourceLimits         `yaml:"resource_limits,omitempty" mapstructure:"resource_limits" json:"resource_limits,omitempty"`
	SourcePaths       map[string]string       `yaml:"source_paths,omitempty" mapstructure:"source_paths" json:"source_paths,omitempty"`
}

// ComputeGeneratorConfig configures the EEST workload exporter.
type ComputeGeneratorConfig struct {
	Image            string    `yaml:"image" mapstructure:"image" json:"image"`
	Tests            []string  `yaml:"tests,omitempty" mapstructure:"tests" json:"tests,omitempty"`
	Filter           string    `yaml:"filter,omitempty" mapstructure:"filter" json:"filter,omitempty"`
	Marker           string    `yaml:"marker" mapstructure:"marker" json:"marker"`
	FixedOpcodeCount []float64 `yaml:"fixed_opcode_count" mapstructure:"fixed_opcode_count" json:"fixed_opcode_count"`
	Families         []string  `yaml:"families,omitempty" mapstructure:"families" json:"families,omitempty"`
	TxGasCap         uint64    `yaml:"tx_gas_cap,omitempty" mapstructure:"tx_gas_cap" json:"tx_gas_cap,omitempty"`
}

// ComputeAnalyzerConfig configures the evm-gasfit analysis invocation.
type ComputeAnalyzerConfig struct {
	Image  string `yaml:"image" mapstructure:"image" json:"image"`
	Config string `yaml:"config" mapstructure:"config" json:"config"`
}

// TimeoutDuration returns the parsed positive compute campaign timeout.
func (c *ComputeConfig) TimeoutDuration() (time.Duration, error) {
	if c == nil {
		return 0, fmt.Errorf("compute configuration is required")
	}

	d, err := time.ParseDuration(c.Timeout)
	if err != nil {
		return 0, fmt.Errorf("compute.timeout: invalid duration %q: %w", c.Timeout, err)
	}

	if d <= 0 {
		return 0, fmt.Errorf("compute.timeout must be positive, got %q", c.Timeout)
	}

	return d, nil
}

// ValidateCompute checks only the compute-campaign configuration. It is
// intentionally separate from Validate because compute campaigns do not use
// execution clients, fixture replay, or rollback strategies.
func (c *Config) ValidateCompute() error {
	if c == nil || c.Compute == nil {
		return fmt.Errorf("compute configuration is required")
	}

	return c.Compute.Validate()
}

// Validate checks the compute-campaign configuration.
func (c *ComputeConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("compute configuration is required")
	}

	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("compute.id is required")
	}

	if strings.TrimSpace(c.ResultsDir) == "" {
		return fmt.Errorf("compute.results_dir is required")
	}

	if strings.TrimSpace(c.WorkerImage) == "" {
		return fmt.Errorf("compute.worker_image is required")
	}

	if strings.TrimSpace(c.Analyzer.Image) == "" {
		return fmt.Errorf("compute.analyzer.image is required")
	}

	if err := validateComputeFile(c.Analyzer.Config, "compute.analyzer.config"); err != nil {
		return err
	}

	if err := ValidateComputeQualificationPolicy(c.Analyzer.Config, c.Engine); err != nil {
		return err
	}

	if !validComputeEngines[c.Engine] {
		return fmt.Errorf(
			"compute.engine: invalid value %q (must be %q or %q)",
			c.Engine, ComputeEngineEvm2, ComputeEngineNewL1,
		)
	}

	if c.ContainerRuntime != "docker" && c.ContainerRuntime != "podman" {
		return fmt.Errorf(
			"compute.container_runtime: invalid value %q (must be \"docker\" or \"podman\")",
			c.ContainerRuntime,
		)
	}

	hasWorkload := strings.TrimSpace(c.Workload) != ""
	hasGenerator := c.Generator != nil
	if hasWorkload == hasGenerator {
		return fmt.Errorf("compute: exactly one of workload or generator is required")
	}

	if hasWorkload {
		if err := validateComputeFile(c.Workload, "compute.workload"); err != nil {
			return err
		}
	} else if err := c.Generator.validate(); err != nil {
		return err
	}

	if c.Sessions < 2 {
		return fmt.Errorf("compute.sessions must be at least 2, got %d", c.Sessions)
	}

	if c.PilotRepetitions < 1 {
		return fmt.Errorf("compute.pilot_repetitions must be at least 1, got %d", c.PilotRepetitions)
	}

	if c.WarmupRepetitions < 1 {
		return fmt.Errorf("compute.warmup_repetitions must be at least 1, got %d", c.WarmupRepetitions)
	}

	if c.Repetitions < 1 {
		return fmt.Errorf("compute.repetitions must be at least 1, got %d", c.Repetitions)
	}

	if _, err := c.TimeoutDuration(); err != nil {
		return err
	}

	if err := c.ResourceLimits.Validate("compute.resource_limits"); err != nil {
		return err
	}

	for name, path := range c.SourcePaths {
		if !validComputeSourcePaths[name] {
			return fmt.Errorf("compute.source_paths: unsupported repository %q", name)
		}
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("compute.source_paths.%s must not be empty", name)
		}
	}

	return nil
}

var validComputeSourcePaths = map[string]bool{
	"benchmarkoor":    true,
	"evm2":            true,
	"newl1":           true,
	"execution_specs": true,
}

// validComputeEngines lists the execution engines a compute campaign can
// measure. pkg/compute binds each engine to its required execution boundary,
// so the two packages must agree on these names.
var validComputeEngines = map[string]bool{
	ComputeEngineEvm2:  true,
	ComputeEngineNewL1: true,
}

var validComputeFamilies = map[string]bool{
	"arithmetic":   true,
	"bitwise":      true,
	"comparison":   true,
	"stack":        true,
	"control_flow": true,
	"keccak":       true,
	"precompile":   true,
}

func (c *ComputeGeneratorConfig) validate() error {
	if strings.TrimSpace(c.Image) == "" {
		return fmt.Errorf("compute.generator.image is required")
	}

	if len(c.FixedOpcodeCount) == 0 {
		return fmt.Errorf("compute.generator.fixed_opcode_count must include at least one positive count")
	}

	hasPositive := false
	for i, count := range c.FixedOpcodeCount {
		if math.IsNaN(count) || math.IsInf(count, 0) || count < 0 {
			return fmt.Errorf(
				"compute.generator.fixed_opcode_count[%d] must be a finite nonnegative number (thousands of opcodes), got %v",
				i, count,
			)
		}
		if count > 0 {
			hasPositive = true
		}
	}
	if !hasPositive {
		return fmt.Errorf("compute.generator.fixed_opcode_count must include at least one positive count")
	}

	if c.TxGasCap > maxComputeTxGasCap {
		return fmt.Errorf(
			"compute.generator.tx_gas_cap %d exceeds the Osaka maximum %d",
			c.TxGasCap, maxComputeTxGasCap,
		)
	}

	seenFamilies := make(map[string]struct{}, len(c.Families))
	for _, family := range c.Families {
		if !validComputeFamilies[family] {
			return fmt.Errorf("compute.generator.families: unsupported Osaka compute family %q", family)
		}
		if _, duplicate := seenFamilies[family]; duplicate {
			return fmt.Errorf("compute.generator.families: duplicate family %q", family)
		}
		seenFamilies[family] = struct{}{}
	}

	return nil
}

func validateComputeFile(path, field string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%s is required", field)
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: reading %q: %w", field, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s: %q must be a file", field, path)
	}

	return nil
}

// ValidateComputeQualificationPolicy verifies that an evm-gasfit configuration
// carries the explicit evidence gates required for a compute campaign, and
// analyzes the campaign engine's rows (its runtimes CSV client name).
func ValidateComputeQualificationPolicy(path, engine string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("compute.analyzer.config: reading %q: %w", path, err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("compute.analyzer.config: parsing YAML %q: %w", path, err)
	}

	clients, _ := document["clients"].([]any)
	hasEngineClient := false
	for _, client := range clients {
		if clientName, ok := client.(string); ok && clientName == engine {
			hasEngineClient = true
			break
		}
	}
	if !hasEngineClient {
		return fmt.Errorf("compute.analyzer.config: campaign analysis requires a clients list containing %q", engine)
	}

	campaign, isCampaign := document["campaign"]
	if !isCampaign {
		return fmt.Errorf("compute.analyzer.config: compute campaigns require a campaign mapping")
	}
	campaignValues, ok := campaign.(map[string]any)
	if !ok {
		return fmt.Errorf("compute.analyzer.config: campaign must be a mapping")
	}
	if err := validateComputeCampaignEligibility(campaignValues); err != nil {
		return err
	}

	qualification, ok := document["qualification"].(map[string]any)
	if !ok {
		return fmt.Errorf("compute.analyzer.config: campaign analysis requires a qualification mapping")
	}
	if err := validateComputeBlockUnqualified(qualification); err != nil {
		return err
	}

	if err := validateQualificationProbability(qualification, "confidence_level"); err != nil {
		return err
	}
	for _, key := range []string{"max_relative_uncertainty", "max_holdout_error"} {
		if err := validatePositiveQualificationGate(qualification, key); err != nil {
			return err
		}
	}
	if err := validateMinimumSessions(qualification); err != nil {
		return err
	}

	return nil
}

func validateComputeCampaignEligibility(campaign map[string]any) error {
	for _, column := range []struct{ key, name string }{
		{"session_column", "session_id"},
		{"phase_column", "phase"},
		{"status_column", "status"},
		{"sample_column", "sample_id"},
		{"correctness_column", "correctness_passed"},
	} {
		if value, present := campaign[column.key]; present && value != column.name {
			return fmt.Errorf("compute.analyzer.config: native compute exports require campaign.%s to be %q", column.key, column.name)
		}
	}
	if values, present := campaign["eligible_phases"]; present {
		phases, ok := campaignEligibilityValues(values)
		if !ok {
			return fmt.Errorf("compute.analyzer.config: campaign.eligible_phases must be a non-empty string or list")
		}
		for _, phase := range phases {
			if phase != "qualification" {
				return fmt.Errorf("compute.analyzer.config: native compute campaigns require campaign.eligible_phases to contain only %q", "qualification")
			}
		}
	}
	if values, present := campaign["eligible_statuses"]; present {
		statuses, ok := campaignEligibilityValues(values)
		if !ok {
			return fmt.Errorf("compute.analyzer.config: campaign.eligible_statuses must be a non-empty string or list")
		}
		for _, status := range statuses {
			if status != "executed" {
				return fmt.Errorf("compute.analyzer.config: native compute campaigns require campaign.eligible_statuses to contain only %q", "executed")
			}
		}
	}
	if value, present := campaign["require_correctness_passed"]; present {
		requireCorrectness, ok := value.(bool)
		if !ok || !requireCorrectness {
			return fmt.Errorf("compute.analyzer.config: native compute campaigns require campaign.require_correctness_passed to be true")
		}
	}
	return nil
}

func validateComputeBlockUnqualified(qualification map[string]any) error {
	if value, present := qualification["block_unqualified"]; present {
		blockUnqualified, ok := value.(bool)
		if !ok || !blockUnqualified {
			return fmt.Errorf("compute.analyzer.config: native compute campaigns require qualification.block_unqualified to be true")
		}
	}
	return nil
}

func campaignEligibilityValues(value any) ([]string, bool) {
	switch values := value.(type) {
	case string:
		if values == "" {
			return nil, false
		}
		return []string{values}, true
	case []any:
		if len(values) == 0 {
			return nil, false
		}
		out := make([]string, len(values))
		for i, value := range values {
			stringValue, ok := value.(string)
			if !ok || stringValue == "" {
				return nil, false
			}
			out[i] = stringValue
		}
		return out, true
	default:
		return nil, false
	}
}

func validateQualificationProbability(qualification map[string]any, key string) error {
	value, ok := qualificationNumber(qualification, key)
	if !ok || value <= 0 || value >= 1 {
		return fmt.Errorf(
			"compute.analyzer.config: campaign analysis requires qualification.%s in (0, 1)",
			key,
		)
	}
	return nil
}

func validatePositiveQualificationGate(qualification map[string]any, key string) error {
	value, ok := qualificationNumber(qualification, key)
	if !ok || value <= 0 {
		return fmt.Errorf(
			"compute.analyzer.config: campaign analysis requires positive qualification.%s",
			key,
		)
	}
	return nil
}

func validateMinimumSessions(qualification map[string]any) error {
	value, ok := qualificationNumber(qualification, "min_sessions")
	if !ok || math.Trunc(value) != value || value < 2 {
		return fmt.Errorf(
			"compute.analyzer.config: campaign analysis requires qualification.min_sessions of at least 2",
		)
	}
	return nil
}

func qualificationNumber(qualification map[string]any, key string) (float64, bool) {
	value, present := qualification[key]
	if !present {
		return 0, false
	}

	var number float64
	switch value := value.(type) {
	case int:
		number = float64(value)
	case int64:
		number = float64(value)
	case uint64:
		number = float64(value)
	case float64:
		number = value
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}
