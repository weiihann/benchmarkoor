package compute

import (
	"bufio"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	gasfitRuntimesFile = "runtimes.csv"
	gasfitOpcountsFile = "opcounts.json"
	gasfitManifestFile = "manifest.json"
	gasfitConfigFile   = "config.yaml"
)

type gasfitInputs struct {
	RuntimesPath string
	OpcountsPath string
	ManifestPath string
	ConfigPath   string
	Hashes       map[string]string
}

type diagnosticCounts struct {
	preparedHash   string
	baselineHash   string
	commitmentHash string
	counts         map[string]uint64
	targetCount    uint64
}

// exportGasfitInputs snapshots canonical campaign artifacts and produces the
// ordinary CSV/opcount inputs consumed by evm-gasfit. It keeps every terminal
// result row in the CSV; only a successful diagnostic with the identical
// prepared artifact can attach operation counts to an executed timing row.
func exportGasfitInputs(runDir, attemptDir, configPath string) (*gasfitInputs, error) {
	workloadPath := filepath.Join(runDir, "workload.json")
	manifestPath := filepath.Join(runDir, "manifest.json")
	samplesPath := filepath.Join(runDir, "samples.jsonl")
	attemptWorkloadPath := filepath.Join(attemptDir, "workload.json")
	attemptSamplesPath := filepath.Join(attemptDir, "samples.jsonl")

	for source, target := range map[string]string{
		configPath:   filepath.Join(attemptDir, gasfitConfigFile),
		manifestPath: filepath.Join(attemptDir, gasfitManifestFile),
		samplesPath:  attemptSamplesPath,
		workloadPath: attemptWorkloadPath,
	} {
		if err := copyAnalysisInput(source, target); err != nil {
			return nil, err
		}
	}
	workload, err := readWorkload(attemptWorkloadPath)
	if err != nil {
		return nil, err
	}
	results, err := readResults(attemptSamplesPath)
	if err != nil {
		return nil, err
	}

	diagnostics, err := collectDiagnosticCounts(workload, results)
	if err != nil {
		return nil, err
	}
	if err := checkTimingDiagnosticLinkage(results, diagnostics); err != nil {
		return nil, err
	}

	runtimesPath := filepath.Join(attemptDir, gasfitRuntimesFile)
	if err := writeGasfitRuntimes(runtimesPath, workload, results); err != nil {
		return nil, err
	}
	opcountsPath := filepath.Join(attemptDir, gasfitOpcountsFile)
	if err := writeGasfitOpcounts(opcountsPath, diagnostics); err != nil {
		return nil, err
	}

	hashes := make(map[string]string, 6)
	for name, path := range map[string]string{
		"config":   filepath.Join(attemptDir, gasfitConfigFile),
		"manifest": filepath.Join(attemptDir, gasfitManifestFile),
		"opcounts": opcountsPath,
		"runtimes": runtimesPath,
		"samples":  attemptSamplesPath,
		"workload": attemptWorkloadPath,
	} {
		hash, hashErr := fileSHA256(path)
		if hashErr != nil {
			return nil, hashErr
		}
		hashes[name] = hash
	}
	return &gasfitInputs{
		RuntimesPath: runtimesPath,
		OpcountsPath: opcountsPath,
		ManifestPath: filepath.Join(attemptDir, gasfitManifestFile),
		ConfigPath:   filepath.Join(attemptDir, gasfitConfigFile),
		Hashes:       hashes,
	}, nil
}

func readWorkload(path string) (*Workload, error) {
	workload, _, err := loadComputeWorkload(path)
	if err != nil {
		return nil, fmt.Errorf("loading workload %q: %w", path, err)
	}
	return workload, nil
}

func readResults(path string) ([]Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening samples %q: %w", path, err)
	}
	defer file.Close()

	results := make([]Result, 0)
	scanner := bufio.NewScanner(file)
	// A diagnostic opcode map can be large for broad workloads.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		if strings.TrimSpace(scanner.Text()) == "" {
			return nil, fmt.Errorf("samples %q line %d is blank", path, line)
		}
		var result Result
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			return nil, fmt.Errorf("decoding samples %q line %d: %w", path, line, err)
		}
		if err := result.Validate(); err != nil {
			return nil, fmt.Errorf("validating samples %q line %d: %w", path, line, err)
		}
		results = append(results, result)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading samples %q: %w", path, err)
	}
	return results, nil
}

func collectDiagnosticCounts(workload *Workload, results []Result) (map[string]diagnosticCounts, error) {
	cases := workloadCases(workload)
	diagnostics := make(map[string]diagnosticCounts)
	for _, result := range results {
		if result.Phase != PhaseDiagnostic || result.Status != ResultStatusExecuted || !result.CorrectnessPassed {
			continue
		}
		workloadCase, ok := cases[result.CaseID]
		if !ok {
			return nil, fmt.Errorf("diagnostic sample %q references unknown case %q", result.SampleID, result.CaseID)
		}
		if result.PreparedHash == nil || result.BaselineHash == nil || result.CommitmentHash == nil || result.TargetCount == nil {
			return nil, fmt.Errorf("diagnostic sample %q has incomplete executed artifact identity", result.SampleID)
		}
		if err := validateTargetCount(workloadCase, result); err != nil {
			return nil, fmt.Errorf("diagnostic sample %q: %w", result.SampleID, err)
		}

		candidate := diagnosticCounts{
			preparedHash:   *result.PreparedHash,
			baselineHash:   *result.BaselineHash,
			commitmentHash: *result.CommitmentHash,
			counts:         cloneCounts(result.OpcodeCounts),
			targetCount:    *result.TargetCount,
		}
		if current, exists := diagnostics[result.CaseID]; exists {
			if !sameDiagnosticCounts(current, candidate) {
				return nil, fmt.Errorf("diagnostic case %q has inconsistent prepared, baseline, commitment, target, or opcode counts", result.CaseID)
			}
			continue
		}
		diagnostics[result.CaseID] = candidate
	}
	return diagnostics, nil
}

func validateTargetCount(workloadCase *WorkloadCase, result Result) error {
	if result.TargetCount == nil {
		return fmt.Errorf("target_count is missing")
	}
	countKey := workloadCase.TargetOperation
	if address, ok := stringParameter(workloadCase.Parameters, "precompile_address"); ok {
		countKey = "PRECOMPILE_" + strings.ToLower(address)
	}
	actual, ok := result.OpcodeCounts[countKey]
	if !ok {
		return fmt.Errorf("opcode_counts is missing target count key %q", countKey)
	}
	if actual != *result.TargetCount {
		return fmt.Errorf("target_count %d disagrees with opcode_counts[%q]=%d", *result.TargetCount, countKey, actual)
	}
	return nil
}

func checkTimingDiagnosticLinkage(results []Result, diagnostics map[string]diagnosticCounts) error {
	for _, result := range results {
		if result.Phase == PhaseDiagnostic || result.Status != ResultStatusExecuted || !result.CorrectnessPassed {
			continue
		}
		diagnostic, ok := diagnostics[result.CaseID]
		if !ok {
			return fmt.Errorf("timing sample %q case %q has no successful diagnostic counts", result.SampleID, result.CaseID)
		}
		if result.PreparedHash == nil || result.BaselineHash == nil || result.CommitmentHash == nil {
			return fmt.Errorf("timing sample %q has incomplete executed artifact identity", result.SampleID)
		}
		if *result.PreparedHash != diagnostic.preparedHash {
			return fmt.Errorf("timing sample %q case %q prepared_hash does not match its diagnostic", result.SampleID, result.CaseID)
		}
		if *result.BaselineHash != diagnostic.baselineHash || *result.CommitmentHash != diagnostic.commitmentHash {
			return fmt.Errorf("timing sample %q case %q has inconsistent baseline or commitment hash", result.SampleID, result.CaseID)
		}
	}
	return nil
}

func writeGasfitRuntimes(path string, workload *Workload, results []Result) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating runtimes CSV %q: %w", path, err)
	}
	defer file.Close()

	parameterNames := primitiveParameterNames(workload)
	header := []string{
		"client_name", "fixture_name", "test_runtime_ms", "session_id", "sample_id",
		"phase", "status", "correctness_passed", "repetition", "execution_boundary",
		"baseline_hash", "prepared_hash", "commitment_hash", "declared_gas", "charged_gas",
		"reason",
	}
	for _, name := range parameterNames {
		header = append(header, "param_"+name)
	}

	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("writing runtimes CSV header: %w", err)
	}
	cases := workloadCases(workload)
	for _, result := range results {
		workloadCase, ok := cases[result.CaseID]
		if !ok {
			return fmt.Errorf("sample %q references unknown case %q", result.SampleID, result.CaseID)
		}
		row := []string{
			SupportedClient, result.CaseID, durationMilliseconds(result.ExecutionDurationNS), result.SessionID,
			result.SampleID, result.Phase, result.Status, strconv.FormatBool(result.CorrectnessPassed),
			strconv.FormatUint(result.Repetition, 10), result.ExecutionBoundary, dereference(result.BaselineHash),
			dereference(result.PreparedHash), dereference(result.CommitmentHash), uintString(result.DeclaredGas),
			uintString(result.ChargedGas), resultReason(result),
		}
		for _, name := range parameterNames {
			row = append(row, primitiveParameterValue(workloadCase.Parameters[name]))
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("writing runtimes CSV row for sample %q: %w", result.SampleID, err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flushing runtimes CSV %q: %w", path, err)
	}
	return nil
}

func writeGasfitOpcounts(path string, diagnostics map[string]diagnosticCounts) error {
	payload := make(map[string]map[string]uint64, len(diagnostics))
	for caseID, diagnostic := range diagnostics {
		counts := cloneCounts(diagnostic.counts)
		// evm-gasfit requires opcount to be the target count. Semantic precompile
		// counters remain available for their explicit count source, but are never
		// summed into a synthetic total.
		counts["opcount"] = diagnostic.targetCount
		payload[caseID] = counts
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding opcounts: %w", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing opcounts %q: %w", path, err)
	}
	return nil
}

func copyAnalysisInput(source, target string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading analysis input %q: %w", source, err)
	}
	if err := os.WriteFile(target, contents, 0o644); err != nil {
		return fmt.Errorf("writing analysis snapshot %q: %w", target, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %q for SHA-256: %w", path, err)
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:]), nil
}

func workloadCases(workload *Workload) map[string]*WorkloadCase {
	cases := make(map[string]*WorkloadCase, len(workload.Cases))
	for i := range workload.Cases {
		cases[workload.Cases[i].ID] = &workload.Cases[i]
	}
	return cases
}

func primitiveParameterNames(workload *Workload) []string {
	found := make(map[string]struct{})
	for i := range workload.Cases {
		for name, value := range workload.Cases[i].Parameters {
			if isPrimitiveParameter(value) {
				found[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isPrimitiveParameter(value any) bool {
	switch value.(type) {
	case string, bool, float64, json.Number, nil:
		return true
	default:
		return false
	}
}

func primitiveParameterValue(value any) string {
	if !isPrimitiveParameter(value) || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func stringParameter(parameters map[string]any, name string) (string, bool) {
	value, ok := parameters[name]
	stringValue, isString := value.(string)
	return stringValue, ok && isString
}

func cloneCounts(counts map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(counts))
	for name, count := range counts {
		cloned[name] = count
	}
	return cloned
}

func sameDiagnosticCounts(left, right diagnosticCounts) bool {
	if left.preparedHash != right.preparedHash || left.baselineHash != right.baselineHash || left.commitmentHash != right.commitmentHash || left.targetCount != right.targetCount || len(left.counts) != len(right.counts) {
		return false
	}
	for name, leftCount := range left.counts {
		if rightCount, ok := right.counts[name]; !ok || rightCount != leftCount {
			return false
		}
	}
	return true
}

func durationMilliseconds(duration *uint64) string {
	if duration == nil {
		return ""
	}
	whole := *duration / 1_000_000
	fraction := *duration % 1_000_000
	if fraction == 0 {
		return strconv.FormatUint(whole, 10)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	return strconv.FormatUint(whole, 10) + "." + fractionText
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func uintString(value *uint64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatUint(*value, 10)
}

func resultReason(result Result) string {
	if result.Error == nil {
		return ""
	}
	return result.Error.Stage + ": " + result.Error.Message
}
