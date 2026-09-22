// Package compute defines the canonical versioned file/JSON contract used by
// the Prague strictly-compute benchmark pipeline: workload packages exported
// by the execution-specs generator, sample requests handed to the NewL1
// execution worker (newl1-bench --request <request.json> --output
// <samples.jsonl>), and the per-sample JSONL results that worker emits.
//
// The Go types are the normative definition of the protocol's field names.
// Matching JSON Schemas and examples live under schema/ so the Python
// generator and the Rust worker bind identical names. The Validate methods
// enforce what JSON Schemas cannot express (identity uniqueness, receipt and
// transaction count agreement, cross-field references, contradictory result
// states) plus the shared vocabulary and encoding rules.
//
// Validation policy highlights:
//
//   - Unsupported workload cases are retained, not rejected: they must carry
//     a reason so coverage gaps stay visible.
//   - Mode and phase applicability: only diagnostic-phase samples may run
//     under the instrumented diagnostic mode; pilot, warmup, and
//     qualification samples run under uninstrumented performance mode.
//     Diagnostic samples may carry measurements but never establish
//     performance eligibility; controllers select qualification inputs from
//     performance-phase rows only.
//   - Failed results distinguish worker infrastructure failure from
//     completed-but-incorrect execution: they must carry an error record but
//     may retain the observed duration, hashes, and gas quantities when the
//     production execution call returned before the oracle failed.
//   - Artifact hash fields (baseline, prepared, commitment) carry SHA-256
//     digests of substantive computed artifacts. Validation checks format
//     only; producing real digests, never constants, is the writer's duty.
//     Unsupported results leave them null rather than fabricating values.
//   - Unavailable counts and gas quantities are intentionally null, never
//     zero. gas_before_settlement in particular must never be derived by
//     switching settlement to charge-by-limit simulation.
//   - Target and opcode counts are diagnostic-only observations, linked to
//     the timed workload through prepared_hash.
//
// Naming and counting conventions that all sides must follow:
//
//   - The 0x20 hashing operation is canonically named KECCAK256, never SHA3;
//     target_operation values and opcode_counts keys use KECCAK256 so the
//     generator, worker, and analysis bind one name.
//   - Precompile target counts are actual executions of the specific
//     precompile address, never aggregate STATICCALL totals: fixed-count
//     outer wrappers can add calls of their own. Cases carry the addressed
//     precompile in parameters.precompile_address (0x-prefixed 20-byte hex).
//     The worker records a semantic target key in opcode_counts alongside
//     the raw opcode mnemonics — PRECOMPILE_<address>, e.g.
//     PRECOMPILE_0x0000000000000000000000000000000000000009 — counting
//     invocations of exactly that address. target_count equals that semantic
//     count, and the gasfit model count_source references the semantic key
//     instead of defaulting to STATICCALL.
package compute

import (
	"errors"
	"fmt"
	"strings"
)

// Protocol version and shared vocabulary.
const (
	// SchemaVersion is the only schema_version every artifact in this
	// contract accepts; any other value is a different protocol.
	SchemaVersion = 1

	// SupportedFork is the fork the compute milestone measures.
	SupportedFork = "Prague"

	// SupportedChain is the only NewL1 chain identifier requests accept.
	SupportedChain = "devnet"

	// ExecutionBoundary names the timed boundary: the synchronous
	// NewL1EvmBlockExecution::execute call covering user transactions, the
	// system tail, and the LtHash state commitment. Any other label
	// describes a different measurement.
	ExecutionBoundary = "newl1_block_execution"
)

// Workload case status values.
const (
	CaseStatusReady       = "ready"
	CaseStatusUnsupported = "unsupported"
)

// Worker execution modes. Diagnostic mode runs an inspector-bearing EVM and
// is the only mode allowed for diagnostic-phase samples; performance mode
// runs the uninstrumented production executor for timing phases.
const (
	ModeDiagnostic  = "diagnostic"
	ModePerformance = "performance"
)

// Sample phases. PhaseDiagnostic pairs with ModeDiagnostic; the timing phases
// pair with ModePerformance.
const (
	PhaseDiagnostic    = "diagnostic"
	PhasePilot         = "pilot"
	PhaseWarmup        = "warmup"
	PhaseQualification = "qualification"
)

// Terminal result statuses.
const (
	ResultStatusExecuted    = "executed"
	ResultStatusFailed      = "failed"
	ResultStatusUnsupported = "unsupported"
)

// maxPrecompileAddress is the highest address Prague binds to a precompile
// (the EIP-2537 BLS12-381 range ends at 0x11). Transactions addressed to
// 0x01..0x11 are exempt from the ready-case rule that the recipient carries
// code in the prestate, because precompiles are implicit accounts.
const maxPrecompileAddress = 0x11

// Generator identifies the software revision and deterministic randomness
// that produced a workload package.
type Generator struct {
	Revision string `json:"revision"`
	Seed     int64  `json:"seed"`
}

// Workload is the root object of a workload file exported by the
// execution-specs generator. Case IDs preserve the originating EEST fixture
// identity so results stay traceable to their source benchmark.
type Workload struct {
	SchemaVersion int            `json:"schema_version"`
	Fork          string         `json:"fork"`
	Generator     Generator      `json:"generator"`
	Cases         []WorkloadCase `json:"cases"`
}

// WorkloadCase is a single benchmark case. Ready cases must be fully
// executable: prestate, transaction intent, and independently specified
// expected outcomes. Unsupported cases carry only identity and a reason.
type WorkloadCase struct {
	ID              string            `json:"id"`
	Family          string            `json:"family"`
	TargetOperation string            `json:"target_operation"`
	Parameters      map[string]any    `json:"parameters"`
	Status          string            `json:"status"`
	Reason          string            `json:"reason,omitempty"`
	Pre             Prestate          `json:"pre"`
	Transactions    []Transaction     `json:"transactions"`
	Expected        *ExpectedOutcomes `json:"expected,omitempty"`
}

// Prestate maps 20-byte address literals to pre-execution accounts, EEST
// style.
type Prestate map[string]Account

// Account is one prestate account. Balance and nonce are hex quantities;
// code and storage entries are hex byte strings. An omitted or "0x" code
// denotes an EOA.
type Account struct {
	Balance string            `json:"balance"`
	Nonce   string            `json:"nonce"`
	Code    string            `json:"code,omitempty"`
	Storage map[string]string `json:"storage"`
}

// Transaction is unsigned transaction intent. The worker constructs valid
// native NewL1 transactions from it; secret keys never leave the prepared
// campaign artifacts.
type Transaction struct {
	Sender    string `json:"sender"`
	SecretKey string `json:"secret_key"`
	To        string `json:"to"`
	Data      string `json:"data"`
	Value     string `json:"value"`
	GasLimit  uint64 `json:"gas_limit"`
}

// ExpectedOutcomes is the independently calculated oracle for a case,
// produced by the generator, not by the executing client.
type ExpectedOutcomes struct {
	Receipts []ExpectedReceipt            `json:"receipts"`
	Storage  map[string]map[string]string `json:"storage,omitempty"`
}

// ExpectedReceipt is the expected outcome of one transaction. Success is a
// pointer so an absent value is distinguishable from false.
type ExpectedReceipt struct {
	Success *bool         `json:"success"`
	Logs    []ExpectedLog `json:"logs,omitempty"`
}

// ExpectedLog is one expected receipt log. Topics are 32-byte words.
type ExpectedLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// Request is the file the controller passes to the worker. One request
// drives one fresh worker process for one session; the worker flushes one
// JSONL result per requested sample.
type Request struct {
	SchemaVersion int             `json:"schema_version"`
	WorkloadPath  string          `json:"workload_path"`
	SessionID     string          `json:"session_id"`
	Mode          string          `json:"mode"`
	Chain         string          `json:"chain"`
	Samples       []RequestSample `json:"samples"`
}

// RequestSample schedules one measurement of one workload case.
type RequestSample struct {
	SampleID   string `json:"sample_id"`
	CaseID     string `json:"case_id"`
	Repetition uint64 `json:"repetition"`
	Phase      string `json:"phase"`
}

// Result is one terminal JSONL sample record emitted by the worker. Null
// numeric fields mean "unavailable", never zero. Only executed records with
// correctness_passed true are candidates for calibration, and only
// performance-phase records establish eligibility.
type Result struct {
	SchemaVersion       int               `json:"schema_version"`
	SessionID           string            `json:"session_id"`
	SampleID            string            `json:"sample_id"`
	CaseID              string            `json:"case_id"`
	Repetition          uint64            `json:"repetition"`
	Phase               string            `json:"phase"`
	Status              string            `json:"status"`
	ExecutionDurationNS *uint64           `json:"execution_duration_ns"`
	ExecutionBoundary   string            `json:"execution_boundary"`
	BaselineHash        *string           `json:"baseline_hash"`
	PreparedHash        *string           `json:"prepared_hash"`
	CommitmentHash      *string           `json:"commitment_hash"`
	CorrectnessPassed   bool              `json:"correctness_passed"`
	TargetCount         *uint64           `json:"target_count"`
	OpcodeCounts        map[string]uint64 `json:"opcode_counts"`
	DeclaredGas         *uint64           `json:"declared_gas"`
	ChargedGas          *uint64           `json:"charged_gas"`
	SystemGas           *uint64           `json:"system_gas"`
	GasBeforeSettlement *uint64           `json:"gas_before_settlement"`
	Error               *ResultError      `json:"error,omitempty"`
}

// ResultError reports where and why a sample ended in a non-executed state.
// For failed results the stage separates infrastructure failure from a
// completed execution whose oracle check failed (for example stage
// "correctness" with the observed duration retained).
type ResultError struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

// Validate checks the workload against the canonical contract: exact schema
// version, supported fork, non-empty generator identity, unique case IDs,
// and per-case rules. Unsupported cases are validated for identity and
// reason only and are retained.
func (w *Workload) Validate() error {
	if w == nil {
		return errors.New("compute: workload is nil")
	}
	if w.SchemaVersion != SchemaVersion {
		return fmt.Errorf("compute: workload schema_version %d is not supported (want %d)", w.SchemaVersion, SchemaVersion)
	}
	if w.Fork != SupportedFork {
		return fmt.Errorf("compute: workload fork %q is not supported (want %q)", w.Fork, SupportedFork)
	}
	if strings.TrimSpace(w.Generator.Revision) == "" {
		return errors.New("compute: workload generator.revision is required")
	}
	if w.Generator.Seed < 0 {
		return fmt.Errorf("compute: workload generator.seed must be non-negative, got %d", w.Generator.Seed)
	}
	if len(w.Cases) == 0 {
		return errors.New("compute: workload must contain at least one case")
	}

	seen := make(map[string]struct{}, len(w.Cases))
	for i := range w.Cases {
		c := &w.Cases[i]
		if c.ID == "" {
			return fmt.Errorf("compute: workload case %d has an empty id", i)
		}
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("compute: duplicate workload case id %q", c.ID)
		}
		seen[c.ID] = struct{}{}
		if err := c.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *WorkloadCase) validate() error {
	switch c.Status {
	case CaseStatusUnsupported:
		if strings.TrimSpace(c.Reason) == "" {
			return fmt.Errorf("compute: workload case %q: unsupported cases must state a reason", c.ID)
		}
		return nil
	case CaseStatusReady:
	default:
		return fmt.Errorf("compute: workload case %q: unknown status %q (want %q or %q)", c.ID, c.Status, CaseStatusReady, CaseStatusUnsupported)
	}

	if c.Reason != "" {
		return fmt.Errorf("compute: workload case %q: ready cases must not carry an unsupported reason", c.ID)
	}
	if strings.TrimSpace(c.Family) == "" {
		return fmt.Errorf("compute: workload case %q: family is required", c.ID)
	}
	if strings.TrimSpace(c.TargetOperation) == "" {
		return fmt.Errorf("compute: workload case %q: target_operation is required", c.ID)
	}
	if c.TargetOperation == "SHA3" {
		return fmt.Errorf("compute: workload case %q: target_operation must use the canonical name %q, not the SHA3 alias", c.ID, "KECCAK256")
	}
	if c.Parameters == nil {
		return fmt.Errorf("compute: workload case %q: parameters object is required", c.ID)
	}
	if len(c.Transactions) == 0 {
		return fmt.Errorf("compute: workload case %q: at least one transaction is required", c.ID)
	}
	if c.Expected == nil {
		return fmt.Errorf("compute: workload case %q: expected outcomes are required", c.ID)
	}

	for addr, acct := range c.Pre {
		if !validAddress(addr) {
			return fmt.Errorf("compute: workload case %q: pre account key %q is not a 0x-prefixed 20-byte address", c.ID, addr)
		}
		if !validQuantity(acct.Balance) {
			return fmt.Errorf("compute: workload case %q: pre account %s balance %q is not a hex quantity", c.ID, addr, acct.Balance)
		}
		if !validQuantity(acct.Nonce) {
			return fmt.Errorf("compute: workload case %q: pre account %s nonce %q is not a hex quantity", c.ID, addr, acct.Nonce)
		}
		if acct.Code != "" && !validByteString(acct.Code) {
			return fmt.Errorf("compute: workload case %q: pre account %s code %q is not a hex byte string", c.ID, addr, acct.Code)
		}
		for slot, value := range acct.Storage {
			if !validByteString(slot) {
				return fmt.Errorf("compute: workload case %q: pre account %s storage slot %q is not a hex byte string", c.ID, addr, slot)
			}
			if !validByteString(value) {
				return fmt.Errorf("compute: workload case %q: pre account %s storage value %q for slot %q is not a hex byte string", c.ID, addr, value, slot)
			}
		}
	}

	for i := range c.Transactions {
		tx := &c.Transactions[i]
		if !validAddress(tx.Sender) {
			return fmt.Errorf("compute: workload case %q: transaction %d sender %q is not a 0x-prefixed 20-byte address", c.ID, i, tx.Sender)
		}
		if !validWord(tx.SecretKey) {
			return fmt.Errorf("compute: workload case %q: transaction %d secret_key %q is not a 0x-prefixed 32-byte value", c.ID, i, tx.SecretKey)
		}
		if !validAddress(tx.To) {
			return fmt.Errorf("compute: workload case %q: transaction %d recipient %q is not a 0x-prefixed 20-byte address", c.ID, i, tx.To)
		}
		// A call target must be executable: deployed code in the prestate,
		// or one of Prague's implicit precompile accounts.
		if !isPrecompileAddress(tx.To) {
			acct, ok := c.Pre[tx.To]
			if !ok || len(acct.Code) <= 2 {
				return fmt.Errorf("compute: workload case %q: transaction %d recipient %q carries no code in pre and is not a precompile", c.ID, i, tx.To)
			}
		}
		if !validByteString(tx.Data) {
			return fmt.Errorf("compute: workload case %q: transaction %d data %q is not a hex byte string", c.ID, i, tx.Data)
		}
		if !validQuantity(tx.Value) {
			return fmt.Errorf("compute: workload case %q: transaction %d value %q is not a hex quantity", c.ID, i, tx.Value)
		}
		if tx.GasLimit == 0 {
			return fmt.Errorf("compute: workload case %q: transaction %d gas_limit must be positive, got 0", c.ID, i)
		}
	}

	exp := c.Expected
	if len(exp.Receipts) != len(c.Transactions) {
		return fmt.Errorf("compute: workload case %q: expected %d receipts for %d transactions", c.ID, len(exp.Receipts), len(c.Transactions))
	}
	for i := range exp.Receipts {
		r := &exp.Receipts[i]
		if r.Success == nil {
			return fmt.Errorf("compute: workload case %q: expected receipt %d must state success", c.ID, i)
		}
		for j := range r.Logs {
			l := &r.Logs[j]
			if !validAddress(l.Address) {
				return fmt.Errorf("compute: workload case %q: expected receipt %d log %d address %q is not a 0x-prefixed 20-byte address", c.ID, i, j, l.Address)
			}
			for k, topic := range l.Topics {
				if !validWord(topic) {
					return fmt.Errorf("compute: workload case %q: expected receipt %d log %d topic %d %q is not a 0x-prefixed 32-byte value", c.ID, i, j, k, topic)
				}
			}
			if !validByteString(l.Data) {
				return fmt.Errorf("compute: workload case %q: expected receipt %d log %d data %q is not a hex byte string", c.ID, i, j, l.Data)
			}
		}
	}
	for addr, slots := range exp.Storage {
		if !validAddress(addr) {
			return fmt.Errorf("compute: workload case %q: expected storage account %q is not a 0x-prefixed 20-byte address", c.ID, addr)
		}
		if _, ok := c.Pre[addr]; !ok {
			return fmt.Errorf("compute: workload case %q: expected storage references account %q absent from pre", c.ID, addr)
		}
		for slot, value := range slots {
			if !validByteString(slot) {
				return fmt.Errorf("compute: workload case %q: expected storage slot %q for account %s is not a hex byte string", c.ID, slot, addr)
			}
			if !validByteString(value) {
				return fmt.Errorf("compute: workload case %q: expected storage value %q for slot %q of account %s is not a hex byte string", c.ID, value, slot, addr)
			}
		}
	}
	return nil
}

// Validate checks the request vocabulary and identity rules: exact schema
// version, supported chain and mode, non-empty session and sample list,
// unique sample IDs, valid phases, and mode/phase applicability.
func (r *Request) Validate() error {
	if r == nil {
		return errors.New("compute: request is nil")
	}
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("compute: request schema_version %d is not supported (want %d)", r.SchemaVersion, SchemaVersion)
	}
	if r.WorkloadPath == "" {
		return errors.New("compute: request workload_path is required")
	}
	if r.SessionID == "" {
		return errors.New("compute: request session_id is required")
	}
	if r.Mode != ModeDiagnostic && r.Mode != ModePerformance {
		return fmt.Errorf("compute: request mode %q is unknown (want %q or %q)", r.Mode, ModeDiagnostic, ModePerformance)
	}
	if r.Chain != SupportedChain {
		return fmt.Errorf("compute: request chain %q is not supported (want %q)", r.Chain, SupportedChain)
	}
	if len(r.Samples) == 0 {
		return errors.New("compute: request must schedule at least one sample")
	}

	seen := make(map[string]struct{}, len(r.Samples))
	for i := range r.Samples {
		s := &r.Samples[i]
		if s.SampleID == "" {
			return fmt.Errorf("compute: request sample %d has an empty sample_id", i)
		}
		if _, dup := seen[s.SampleID]; dup {
			return fmt.Errorf("compute: duplicate request sample_id %q", s.SampleID)
		}
		seen[s.SampleID] = struct{}{}
		if s.CaseID == "" {
			return fmt.Errorf("compute: request sample %q has an empty case_id", s.SampleID)
		}
		if !validPhase(s.Phase) {
			return fmt.Errorf("compute: request sample %q has unknown phase %q", s.SampleID, s.Phase)
		}
		if err := checkModePhaseApplicability(r.Mode, s.Phase, s.SampleID); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAgainst cross-checks a request against the workload it references:
// both must individually validate, and every scheduled sample must reference
// a case the workload contains. Referencing an unsupported case is allowed;
// the worker then reports an unsupported result for it.
func (r *Request) ValidateAgainst(w *Workload) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := w.Validate(); err != nil {
		return err
	}
	index := make(map[string]struct{}, len(w.Cases))
	for i := range w.Cases {
		index[w.Cases[i].ID] = struct{}{}
	}
	for i := range r.Samples {
		s := &r.Samples[i]
		if _, ok := index[s.CaseID]; !ok {
			return fmt.Errorf("compute: request sample %q references workload case %q that does not exist", s.SampleID, s.CaseID)
		}
	}
	return nil
}

// Validate checks one result record for contradictory states:
//
//   - executed: correctness passed, no error, valid artifact hashes,
//     declared and charged gas present, a non-null positive duration for
//     performance phases, and a target count in diagnostic phase.
//   - failed: an error record with stage and message; observed duration,
//     hashes, and gas may be retained when execution returned first.
//   - unsupported: an error record, no duration, no correctness claim, and
//     no fabricated hashes, counts, or gas quantities.
//
// Target and opcode counts are rejected outside diagnostic phase for every
// status.
func (res *Result) Validate() error {
	if res == nil {
		return errors.New("compute: result is nil")
	}
	if res.SchemaVersion != SchemaVersion {
		return fmt.Errorf("compute: result schema_version %d is not supported (want %d)", res.SchemaVersion, SchemaVersion)
	}
	if res.SessionID == "" {
		return errors.New("compute: result session_id is required")
	}
	if res.SampleID == "" {
		return errors.New("compute: result sample_id is required")
	}
	if res.CaseID == "" {
		return errors.New("compute: result case_id is required")
	}
	if !validPhase(res.Phase) {
		return fmt.Errorf("compute: result %q has unknown phase %q", res.SampleID, res.Phase)
	}
	if res.ExecutionBoundary != ExecutionBoundary {
		return fmt.Errorf("compute: result %q execution_boundary %q is not the required boundary %q", res.SampleID, res.ExecutionBoundary, ExecutionBoundary)
	}

	switch res.Status {
	case ResultStatusExecuted:
		if !res.CorrectnessPassed {
			return fmt.Errorf("compute: result %q: executed results must pass correctness checks", res.SampleID)
		}
		if res.Error != nil {
			return fmt.Errorf("compute: result %q: executed results must not carry an error record", res.SampleID)
		}
		for field, hash := range map[string]*string{
			"baseline_hash":   res.BaselineHash,
			"prepared_hash":   res.PreparedHash,
			"commitment_hash": res.CommitmentHash,
		} {
			if hash == nil {
				return fmt.Errorf("compute: result %q: executed results must carry %s", res.SampleID, field)
			}
			if !validArtifactHash(*hash) {
				return fmt.Errorf("compute: result %q: %s %q is not a SHA-256 hex digest", res.SampleID, field, *hash)
			}
		}
		if res.DeclaredGas == nil {
			return fmt.Errorf("compute: result %q: executed results must carry declared_gas", res.SampleID)
		}
		if res.ChargedGas == nil {
			return fmt.Errorf("compute: result %q: executed results must carry charged_gas", res.SampleID)
		}
		if res.Phase != PhaseDiagnostic {
			// Timing phases establish eligibility, so their duration must
			// be an actual positive measurement, not an unavailable null.
			if res.ExecutionDurationNS == nil {
				return fmt.Errorf("compute: result %q: executed %s-phase results must carry execution_duration_ns", res.SampleID, res.Phase)
			}
			if *res.ExecutionDurationNS == 0 {
				return fmt.Errorf("compute: result %q: executed %s-phase results must have a positive execution_duration_ns", res.SampleID, res.Phase)
			}
		}
		if res.Phase == PhaseDiagnostic && res.TargetCount == nil {
			return fmt.Errorf("compute: result %q: executed diagnostic-phase results must report target_count", res.SampleID)
		}
	case ResultStatusFailed:
		if res.Error == nil {
			return fmt.Errorf("compute: result %q: failed results must carry an error record", res.SampleID)
		}
		if strings.TrimSpace(res.Error.Stage) == "" || strings.TrimSpace(res.Error.Message) == "" {
			return fmt.Errorf("compute: result %q: failed result error must state both stage and message", res.SampleID)
		}
	case ResultStatusUnsupported:
		if res.Error == nil {
			return fmt.Errorf("compute: result %q: unsupported results must carry an error record", res.SampleID)
		}
		if strings.TrimSpace(res.Error.Stage) == "" || strings.TrimSpace(res.Error.Message) == "" {
			return fmt.Errorf("compute: result %q: unsupported result error must state both stage and message", res.SampleID)
		}
		if res.ExecutionDurationNS != nil {
			return fmt.Errorf("compute: result %q: unsupported results cannot carry an execution duration", res.SampleID)
		}
		if res.CorrectnessPassed {
			return fmt.Errorf("compute: result %q: unsupported results cannot claim correctness", res.SampleID)
		}
		if res.BaselineHash != nil || res.PreparedHash != nil || res.CommitmentHash != nil {
			return fmt.Errorf("compute: result %q: unsupported results must not fabricate artifact hashes", res.SampleID)
		}
		if res.TargetCount != nil || res.OpcodeCounts != nil {
			return fmt.Errorf("compute: result %q: unsupported results cannot carry counts", res.SampleID)
		}
		if res.DeclaredGas != nil || res.ChargedGas != nil || res.SystemGas != nil || res.GasBeforeSettlement != nil {
			return fmt.Errorf("compute: result %q: unsupported results cannot carry gas quantities", res.SampleID)
		}
	default:
		return fmt.Errorf("compute: result %q has unknown status %q (want %q, %q, or %q)", res.SampleID, res.Status, ResultStatusExecuted, ResultStatusFailed, ResultStatusUnsupported)
	}

	if res.Phase != PhaseDiagnostic {
		if res.TargetCount != nil || res.OpcodeCounts != nil {
			return fmt.Errorf("compute: result %q: target_count and opcode_counts are diagnostic-only (phase %q)", res.SampleID, res.Phase)
		}
	}
	if _, ok := res.OpcodeCounts["SHA3"]; ok {
		return fmt.Errorf("compute: result %q: opcode_counts key %q must use the canonical name KECCAK256", res.SampleID, "SHA3")
	}
	return nil
}

func validPhase(phase string) bool {
	switch phase {
	case PhaseDiagnostic, PhasePilot, PhaseWarmup, PhaseQualification:
		return true
	}
	return false
}

// checkModePhaseApplicability keeps mode and phase coherent: the instrumented
// diagnostic mode only runs diagnostic samples, and the timing phases only
// run under uninstrumented performance mode.
func checkModePhaseApplicability(mode, phase, sampleID string) error {
	switch mode {
	case ModeDiagnostic:
		if phase != PhaseDiagnostic {
			return fmt.Errorf("compute: request sample %q: %s-phase samples cannot run in %s mode", sampleID, phase, ModeDiagnostic)
		}
	case ModePerformance:
		if phase == PhaseDiagnostic {
			return fmt.Errorf("compute: request sample %q: diagnostic-phase samples require %s mode", sampleID, ModeDiagnostic)
		}
	}
	return nil
}

func hasHexPrefix(s string) bool {
	return len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
}

func allHexDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// validByteString accepts 0x-prefixed hex with whole bytes ("0x" is empty).
// EEST pads quantities to 32 bytes, but shorter even-length forms decode to
// the same bytes and stay valid.
func validByteString(s string) bool {
	return hasHexPrefix(s) && len(s)%2 == 0 && allHexDigits(s[2:])
}

// validQuantity accepts a 0x-prefixed hex quantity with at least one digit.
// Leading zeros are tolerated because EEST pads values.
func validQuantity(s string) bool {
	return hasHexPrefix(s) && len(s) > 2 && allHexDigits(s[2:])
}

// validAddress accepts a 0x-prefixed 20-byte address.
func validAddress(s string) bool {
	return hasHexPrefix(s) && len(s) == 42 && allHexDigits(s[2:])
}

// validWord accepts a 0x-prefixed 32-byte value (private keys, log topics).
func validWord(s string) bool {
	return hasHexPrefix(s) && len(s) == 66 && allHexDigits(s[2:])
}

// validArtifactHash accepts a SHA-256 digest as 64 hex digits with an
// optional 0x prefix, matching both hashlib hexdigest and 0x-style writers.
func validArtifactHash(s string) bool {
	digits := s
	if hasHexPrefix(s) {
		digits = s[2:]
	}
	return len(digits) == 64 && allHexDigits(digits)
}

// isPrecompileAddress reports whether addr, already validated as a 20-byte
// literal, addresses one of Prague's precompiles (0x01 through 0x11).
func isPrecompileAddress(addr string) bool {
	for i := 2; i < 40; i++ {
		if addr[i] != '0' {
			return false
		}
	}
	last, ok := hexByte(addr[40], addr[41])
	return ok && last >= 1 && last <= maxPrecompileAddress
}

func hexByte(hi, lo byte) (byte, bool) {
	h, okHi := hexNibble(hi)
	l, okLo := hexNibble(lo)
	return h<<4 | l, okHi && okLo
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
