package compute

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	addCaseID    = "compute/instruction/test_arithmetic.py__test_add[u_int8-count_1000]"
	modCaseID    = "compute/instruction/test_arithmetic.py__test_mod[mod_int128-count_1000]"
	contractAddr = "0x7b5a41b2bc1ce4de7c8b1e7b1c4db01c3e9fa111"
	senderAddr   = "0xa94f5374fce5edbc8e2a8697c15331677e6ebf0b"
	baselineHex  = "5f2b9c41d8e07a63b4c1f9d2e8a30756c9b1e4d7f3a28c05b6e9d1f4a7c3b8e2"
	preparedHex  = "7d1e4f8b2c6a9305e8f1b4d7c2a5e8f103b6d9c2e5f8a1b4d7c0e3f6a9b2c5d8"
	commitHex    = "9a3c6e1f8b4d7c2a5e0f3b6d9c1a4e7f2b5d8c0a3e6f1b4d7c2a5e8f103b6d9c"
)

func u64(v uint64) *uint64 { return &v }

func strPtr(v string) *string { return &v }

func boolPtr(v bool) *bool { return &v }

func slotOne() string  { return "0x" + strings.Repeat("00", 31) + "01" }
func valueTwo() string { return "0x" + strings.Repeat("00", 31) + "02" }

func baseWorkload() *Workload {
	return &Workload{
		SchemaVersion: SchemaVersion,
		Fork:          SupportedFork,
		Generator:     Generator{Revision: "ad202d76b52c074ffa9395bd69a72f2a12ad1784", Seed: 20260921},
		Cases: []WorkloadCase{{
			ID:              addCaseID,
			Family:          "arithmetic",
			TargetOperation: "ADD",
			Parameters:      map[string]any{"operand_class": "u_int8", "requested_count": 1000},
			Status:          CaseStatusReady,
			Pre: Prestate{
				contractAddr: {Balance: "0x0", Nonce: "0x0", Code: "0x6001600101", Storage: map[string]string{slotOne(): valueTwo()}},
				senderAddr:   {Balance: "0x30d40", Nonce: "0x0", Storage: map[string]string{}},
			},
			Transactions: []Transaction{{
				Sender:   senderAddr,
				To:       contractAddr,
				Data:     "0x",
				Value:    "0x0",
				GasLimit: 10000000,
			}},
			Expected: &ExpectedOutcomes{
				Receipts: []ExpectedReceipt{{Success: boolPtr(true), Logs: []ExpectedLog{{
					Address: contractAddr,
					Topics:  []string{"0x" + strings.Repeat("ab", 32)},
					Data:    "0x" + strings.Repeat("01", 32),
				}}}},
				Storage: map[string]map[string]string{
					contractAddr: {slotOne(): valueTwo()},
				},
			},
		}},
	}
}

func unsupportedCase() WorkloadCase {
	return WorkloadCase{
		ID:              modCaseID,
		Family:          "arithmetic",
		TargetOperation: "MOD",
		Status:          CaseStatusUnsupported,
		Reason:          "data-dependent MOD has no fixed-count variant",
	}
}

func baseRequest() *Request {
	return &Request{
		SchemaVersion: SchemaVersion,
		WorkloadPath:  "workloads/osaka-compute.json",
		SessionID:     "sess-20260921-0001",
		Mode:          ModeDiagnostic,
		Samples: []RequestSample{
			{SampleID: "s-0001", CaseID: addCaseID, Repetition: 0, Phase: PhaseDiagnostic},
		},
	}
}

func baseResult() *Result {
	return &Result{
		SchemaVersion:       SchemaVersion,
		SessionID:           "sess-20260921-0001",
		SampleID:            "s-0001",
		CaseID:              addCaseID,
		Repetition:          0,
		Phase:               PhaseDiagnostic,
		Status:              ResultStatusExecuted,
		ExecutionDurationNS: u64(482135000),
		ExecutionBoundary:   BoundaryEvm2TransactionExecution,
		BaselineHash:        strPtr(baselineHex),
		PreparedHash:        strPtr(preparedHex),
		CommitmentHash:      strPtr(commitHex),
		CorrectnessPassed:   true,
		TargetCount:         u64(1000),
		OpcodeCounts:        map[string]uint64{"ADD": 1000, "PUSH1": 6},
		DeclaredGas:         u64(903000),
		ChargedGas:          u64(897542),
	}
}

func TestWorkloadValidateAcceptsBaseWorkload(t *testing.T) {
	assert.NoError(t, baseWorkload().Validate())
}

func TestWorkloadValidateRetainsUnsupportedRows(t *testing.T) {
	w := baseWorkload()
	w.Cases = append(w.Cases, unsupportedCase())
	require.NoError(t, w.Validate())

	encoded, err := json.Marshal(w)
	require.NoError(t, err)
	var decoded Workload
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	require.NoError(t, decoded.Validate())
	require.Len(t, decoded.Cases, 2)
	assert.Equal(t, CaseStatusUnsupported, decoded.Cases[1].Status)
	assert.NotEmpty(t, decoded.Cases[1].Reason)
}

func TestWorkloadValidateRejectsVersionAndForkMismatches(t *testing.T) {
	for name, mutate := range map[string]func(*Workload){
		"schema_version zero":   func(w *Workload) { w.SchemaVersion = 0 },
		"schema_version future": func(w *Workload) { w.SchemaVersion = SchemaVersion + 1 },
		"fork prague":           func(w *Workload) { w.Fork = "Prague" },
		"fork empty":            func(w *Workload) { w.Fork = "" },
	} {
		t.Run(name, func(t *testing.T) {
			w := baseWorkload()
			mutate(w)
			assert.Error(t, w.Validate())
		})
	}
}

func TestWorkloadValidateRejectsMalformedGenerator(t *testing.T) {
	for name, mutate := range map[string]func(*Workload){
		"missing revision": func(w *Workload) { w.Generator.Revision = "" },
		"negative seed":    func(w *Workload) { w.Generator.Seed = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			w := baseWorkload()
			mutate(w)
			assert.Error(t, w.Validate())
		})
	}
}

func TestWorkloadValidateRejectsDuplicateCaseIDs(t *testing.T) {
	w := baseWorkload()
	w.Cases = append(w.Cases, baseWorkload().Cases[0])
	err := w.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "duplicate workload case id")
}

func TestWorkloadValidateRejectsEmptyCaseList(t *testing.T) {
	w := baseWorkload()
	w.Cases = nil
	assert.Error(t, w.Validate())
}

func TestWorkloadValidateRejectsUnsupportedCaseWithoutReason(t *testing.T) {
	w := baseWorkload()
	c := unsupportedCase()
	c.Reason = "  "
	w.Cases = append(w.Cases, c)
	err := w.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "must state a reason")
}

func TestWorkloadValidateRejectsReadyCaseWithReason(t *testing.T) {
	w := baseWorkload()
	w.Cases[0].Reason = "not actually runnable"
	err := w.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "must not carry an unsupported reason")
}

func TestWorkloadValidateRejectsUnknownCaseStatus(t *testing.T) {
	w := baseWorkload()
	w.Cases[0].Status = "pending"
	err := w.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown status")
}

func TestWorkloadValidateRejectsMissingReadyCaseFields(t *testing.T) {
	for name, mutate := range map[string]func(*WorkloadCase){
		"missing family":           func(c *WorkloadCase) { c.Family = "" },
		"missing target_operation": func(c *WorkloadCase) { c.TargetOperation = "" },
		"sha3 alias":               func(c *WorkloadCase) { c.TargetOperation = "SHA3" },
		"missing parameters":       func(c *WorkloadCase) { c.Parameters = nil },
		"no transactions":          func(c *WorkloadCase) { c.Transactions = nil },
		"missing expected":         func(c *WorkloadCase) { c.Expected = nil },
	} {
		t.Run(name, func(t *testing.T) {
			w := baseWorkload()
			mutate(&w.Cases[0])
			assert.Error(t, w.Validate())
		})
	}
}

func TestWorkloadValidateRejectsMalformedHex(t *testing.T) {
	for name, mutate := range map[string]func(*Workload){
		"pre key not an address": func(w *Workload) {
			w.Cases[0].Pre["0x1234"] = Account{Balance: "0x0", Nonce: "0x0", Storage: map[string]string{}}
		},
		"balance without 0x": func(w *Workload) {
			acct := w.Cases[0].Pre[senderAddr]
			acct.Balance = "1000"
			w.Cases[0].Pre[senderAddr] = acct
		},
		"nonce empty quantity": func(w *Workload) {
			acct := w.Cases[0].Pre[senderAddr]
			acct.Nonce = "0x"
			w.Cases[0].Pre[senderAddr] = acct
		},
		"code odd nibbles": func(w *Workload) {
			acct := w.Cases[0].Pre[contractAddr]
			acct.Code = "0x601"
			w.Cases[0].Pre[contractAddr] = acct
		},
		"storage slot odd":         func(w *Workload) { w.Cases[0].Pre[contractAddr].Storage["0x1"] = "0x02" },
		"storage value not hex":    func(w *Workload) { w.Cases[0].Pre[contractAddr].Storage[slotOne()] = "zz" },
		"sender not an address":    func(w *Workload) { w.Cases[0].Transactions[0].Sender = "0x1234" },
		"recipient not an address": func(w *Workload) { w.Cases[0].Transactions[0].To = "0000" },
		"data odd nibbles":         func(w *Workload) { w.Cases[0].Transactions[0].Data = "0x123" },
		"value not hex quantity":   func(w *Workload) { w.Cases[0].Transactions[0].Value = "12" },
		"zero gas limit":           func(w *Workload) { w.Cases[0].Transactions[0].GasLimit = 0 },
		"recipient lacks code":     func(w *Workload) { w.Cases[0].Transactions[0].To = senderAddr },
	} {
		t.Run(name, func(t *testing.T) {
			w := baseWorkload()
			mutate(w)
			assert.Error(t, w.Validate())
		})
	}
}

func TestWorkloadValidateAllowsPrecompileRecipientWithoutPrestateCode(t *testing.T) {
	w := baseWorkload()
	w.Cases[0].Transactions[0].To = "0x0000000000000000000000000000000000000009"
	assert.NoError(t, w.Validate())
}

func TestWorkloadValidateAllowsOsakaP256Precompile(t *testing.T) {
	w := baseWorkload()
	w.Cases[0].Transactions[0].To = "0x0000000000000000000000000000000000000100"
	assert.NoError(t, w.Validate())
}

func TestWorkloadValidateRejectsReceiptOutcomeMismatches(t *testing.T) {
	for name, mutate := range map[string]func(*Workload){
		"receipt count mismatch": func(w *Workload) {
			w.Cases[0].Expected.Receipts = append(w.Cases[0].Expected.Receipts, ExpectedReceipt{Success: boolPtr(true)})
		},
		"receipt success unspecified": func(w *Workload) {
			w.Cases[0].Expected.Receipts[0].Success = nil
		},
		"log address malformed": func(w *Workload) {
			w.Cases[0].Expected.Receipts[0].Logs[0].Address = "0xdeadbeef"
		},
		"log topic short": func(w *Workload) {
			w.Cases[0].Expected.Receipts[0].Logs[0].Topics[0] = "0xab"
		},
		"log data odd nibbles": func(w *Workload) {
			w.Cases[0].Expected.Receipts[0].Logs[0].Data = "0x0"
		},
		"expected storage for unknown account": func(w *Workload) {
			w.Cases[0].Expected.Storage = map[string]map[string]string{
				"0x1111111111111111111111111111111111111111": {slotOne(): valueTwo()},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := baseWorkload()
			mutate(w)
			assert.Error(t, w.Validate())
		})
	}
}

func TestRequestValidateAcceptsBaseRequest(t *testing.T) {
	require.NoError(t, baseRequest().Validate())

	perf := baseRequest()
	perf.Mode = ModePerformance
	perf.Samples = []RequestSample{
		{SampleID: "s-0002", CaseID: addCaseID, Repetition: 0, Phase: PhasePilot},
		{SampleID: "s-0003", CaseID: addCaseID, Repetition: 1, Phase: PhaseQualification},
	}
	assert.NoError(t, perf.Validate())
}

func TestRequestValidateRejectsVocabularyMismatches(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"schema_version future": func(r *Request) { r.SchemaVersion = SchemaVersion + 1 },
		"unknown mode":          func(r *Request) { r.Mode = "instrumented" },
		"empty workload path":   func(r *Request) { r.WorkloadPath = "" },
		"empty session id":      func(r *Request) { r.SessionID = "" },
		"no samples":            func(r *Request) { r.Samples = nil },
		"unknown phase": func(r *Request) {
			r.Samples[0].Phase = "calibration"
		},
		"empty sample id": func(r *Request) {
			r.Samples[0].SampleID = ""
		},
		"empty case id": func(r *Request) {
			r.Samples[0].CaseID = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := baseRequest()
			mutate(r)
			assert.Error(t, r.Validate())
		})
	}
}

func TestRequestValidateRejectsDuplicateSampleIDs(t *testing.T) {
	r := baseRequest()
	r.Samples = append(r.Samples, RequestSample{SampleID: "s-0001", CaseID: addCaseID, Phase: PhaseDiagnostic})
	err := r.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "duplicate request sample_id")
}

func TestRequestValidateRejectsModePhasePairingViolations(t *testing.T) {
	diag := baseRequest()
	diag.Samples[0].Phase = PhasePilot
	err := diag.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "cannot run in diagnostic mode")

	perf := baseRequest()
	perf.Mode = ModePerformance
	perf.Samples[0].Phase = PhaseDiagnostic
	err = perf.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "require diagnostic mode")
}

func TestRequestValidateAgainstWorkload(t *testing.T) {
	w := baseWorkload()
	w.Cases = append(w.Cases, unsupportedCase())

	r := baseRequest()
	// Scheduling an unsupported case is legitimate: the worker answers with
	// an unsupported result instead of executing it.
	r.Samples = append(r.Samples, RequestSample{SampleID: "s-0002", CaseID: modCaseID, Phase: PhaseDiagnostic})
	assert.NoError(t, r.ValidateAgainst(w))

	r.Samples[1].CaseID = "compute/instruction/test_missing.py__test_absent"
	err := r.ValidateAgainst(w)
	require.Error(t, err)
	assert.ErrorContains(t, err, "does not exist")
}

func TestResultValidateAcceptsValidResults(t *testing.T) {
	t.Run("diagnostic executed", func(t *testing.T) {
		assert.NoError(t, baseResult().Validate())
	})

	t.Run("performance executed", func(t *testing.T) {
		res := baseResult()
		res.Phase = PhaseQualification
		res.TargetCount = nil
		res.OpcodeCounts = nil
		assert.NoError(t, res.Validate())
	})

	t.Run("failed retains observed duration and hashes", func(t *testing.T) {
		res := baseResult()
		res.Phase = PhaseWarmup
		res.TargetCount = nil
		res.OpcodeCounts = nil
		res.Status = ResultStatusFailed
		res.CorrectnessPassed = false
		res.Error = &ResultError{Stage: "correctness", Message: "witness mismatch: expected log topic differs"}
		assert.NoError(t, res.Validate())
	})

	t.Run("unsupported minimal", func(t *testing.T) {
		res := &Result{
			SchemaVersion:     SchemaVersion,
			SessionID:         "sess-20260921-0001",
			SampleID:          "s-0009",
			CaseID:            modCaseID,
			Phase:             PhasePilot,
			Status:            ResultStatusUnsupported,
			ExecutionBoundary: BoundaryEvm2TransactionExecution,
			Error:             &ResultError{Stage: "workload", Message: "case marked unsupported by generator: data-dependent MOD has no fixed-count variant"},
		}
		assert.NoError(t, res.Validate())
	})
}

func TestResultValidateRejectsContradictoryExecutedResults(t *testing.T) {
	for name, mutate := range map[string]func(*Result){
		"correctness not passed": func(r *Result) { r.CorrectnessPassed = false },
		"error record present": func(r *Result) {
			r.Error = &ResultError{Stage: "late", Message: "boom"}
		},
		"missing baseline hash":   func(r *Result) { r.BaselineHash = nil },
		"missing prepared hash":   func(r *Result) { r.PreparedHash = nil },
		"missing commitment hash": func(r *Result) { r.CommitmentHash = nil },
		"malformed baseline hash": func(r *Result) { r.BaselineHash = strPtr("0x1234") },
		"missing declared gas":    func(r *Result) { r.DeclaredGas = nil },
		"missing charged gas":     func(r *Result) { r.ChargedGas = nil },
		"diagnostic without target count": func(r *Result) {
			r.TargetCount = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			res := baseResult()
			mutate(res)
			assert.Error(t, res.Validate())
		})
	}
}

func TestResultValidateRequiresPositiveDurationInTimingPhases(t *testing.T) {
	perf := baseResult()
	perf.Phase = PhaseQualification
	perf.TargetCount = nil
	perf.OpcodeCounts = nil
	require.NoError(t, perf.Validate())

	missing := *perf
	missing.ExecutionDurationNS = nil
	assert.Error(t, missing.Validate())

	zero := *perf
	zero.ExecutionDurationNS = u64(0)
	assert.Error(t, zero.Validate())

	// Diagnostic execution may carry a measurement but is not required to.
	diag := baseResult()
	diag.ExecutionDurationNS = nil
	assert.NoError(t, diag.Validate())
}

func TestResultValidateRejectsFailedWithoutCompleteError(t *testing.T) {
	res := baseResult()
	res.Phase = PhaseWarmup
	res.TargetCount = nil
	res.OpcodeCounts = nil
	res.Status = ResultStatusFailed
	res.CorrectnessPassed = false

	assert.Error(t, res.Validate(), "failed without error record")

	res.Error = &ResultError{Stage: "correctness", Message: ""}
	assert.Error(t, res.Validate(), "failed with empty error message")
}

func TestResultValidateRejectsContradictoryUnsupportedResults(t *testing.T) {
	base := func() *Result {
		return &Result{
			SchemaVersion:     SchemaVersion,
			SessionID:         "sess-20260921-0001",
			SampleID:          "s-0009",
			CaseID:            modCaseID,
			Phase:             PhasePilot,
			Status:            ResultStatusUnsupported,
			ExecutionBoundary: BoundaryEvm2TransactionExecution,
			Error:             &ResultError{Stage: "workload", Message: "generator marked case unsupported"},
		}
	}

	for name, mutate := range map[string]func(*Result){
		"missing error record":     func(r *Result) { r.Error = nil },
		"carries duration":         func(r *Result) { r.ExecutionDurationNS = u64(1000) },
		"claims correctness":       func(r *Result) { r.CorrectnessPassed = true },
		"fabricated baseline hash": func(r *Result) { r.BaselineHash = strPtr(baselineHex) },
		"fabricated prepared hash": func(r *Result) { r.PreparedHash = strPtr(preparedHex) },
		"carries target count":     func(r *Result) { r.TargetCount = u64(7) },
		"carries opcode counts": func(r *Result) {
			r.OpcodeCounts = map[string]uint64{"ADD": 7}
		},
		"carries declared gas": func(r *Result) { r.DeclaredGas = u64(30000) },
	} {
		t.Run(name, func(t *testing.T) {
			res := base()
			mutate(res)
			assert.Error(t, res.Validate())
		})
	}
}

func TestResultValidateRejectsCountsOutsideDiagnostics(t *testing.T) {
	for name, mutate := range map[string]func(*Result){
		"target count in qualification": func(r *Result) { r.TargetCount = u64(1000) },
		"opcode counts in warmup": func(r *Result) {
			r.OpcodeCounts = map[string]uint64{"ADD": 1000}
		},
	} {
		t.Run(name, func(t *testing.T) {
			res := baseResult()
			res.Phase = PhaseWarmup
			res.TargetCount = nil
			res.OpcodeCounts = nil
			mutate(res)
			assert.Error(t, res.Validate())
		})
	}
}

func TestResultValidateRejectsSHA3OpcodeCountKey(t *testing.T) {
	res := baseResult()
	res.OpcodeCounts = map[string]uint64{"SHA3": 12}
	err := res.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "KECCAK256")
}

func TestResultValidateRejectsUnknownVocabulary(t *testing.T) {
	for name, mutate := range map[string]func(*Result){
		"schema_version future": func(r *Result) { r.SchemaVersion = SchemaVersion + 1 },
		"unknown status":        func(r *Result) { r.Status = "cancelled" },
		"unknown phase":         func(r *Result) { r.Phase = "calibration" },
		"wrong boundary":        func(r *Result) { r.ExecutionBoundary = "revm_interpreter" },
		"empty session id":      func(r *Result) { r.SessionID = "" },
		"empty sample id":       func(r *Result) { r.SampleID = "" },
		"empty case id":         func(r *Result) { r.CaseID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			res := baseResult()
			mutate(res)
			assert.Error(t, res.Validate())
		})
	}
}

func TestJSONFieldNamesMatchContract(t *testing.T) {
	t.Run("workload", func(t *testing.T) {
		encoded, err := json.Marshal(baseWorkload())
		require.NoError(t, err)
		var top map[string]any
		require.NoError(t, json.Unmarshal(encoded, &top))

		for _, key := range []string{"schema_version", "fork", "generator", "cases"} {
			assert.Contains(t, top, key)
		}
		gen := top["generator"].(map[string]any)
		assert.Contains(t, gen, "revision")
		assert.Contains(t, gen, "seed")

		first := top["cases"].([]any)[0].(map[string]any)
		for _, key := range []string{"id", "family", "target_operation", "parameters", "status", "pre", "transactions", "expected"} {
			assert.Contains(t, first, key)
		}
		acct := first["pre"].(map[string]any)[contractAddr].(map[string]any)
		for _, key := range []string{"balance", "nonce", "storage"} {
			assert.Contains(t, acct, key)
		}
		tx := first["transactions"].([]any)[0].(map[string]any)
		for _, key := range []string{"sender", "to", "data", "value", "gas_limit"} {
			assert.Contains(t, tx, key)
		}
		exp := first["expected"].(map[string]any)
		assert.Contains(t, exp, "receipts")
		rcpt := exp["receipts"].([]any)[0].(map[string]any)
		assert.Contains(t, rcpt, "success")
	})

	t.Run("request", func(t *testing.T) {
		encoded, err := json.Marshal(baseRequest())
		require.NoError(t, err)
		var top map[string]any
		require.NoError(t, json.Unmarshal(encoded, &top))

		for _, key := range []string{"schema_version", "workload_path", "session_id", "mode", "samples"} {
			assert.Contains(t, top, key)
		}
		sample := top["samples"].([]any)[0].(map[string]any)
		for _, key := range []string{"sample_id", "case_id", "repetition", "phase"} {
			assert.Contains(t, sample, key)
		}
	})

	t.Run("result", func(t *testing.T) {
		encoded, err := json.Marshal(baseResult())
		require.NoError(t, err)
		var top map[string]any
		require.NoError(t, json.Unmarshal(encoded, &top))

		for _, key := range []string{
			"schema_version", "session_id", "sample_id", "case_id", "repetition",
			"phase", "status", "execution_duration_ns", "execution_boundary",
			"baseline_hash", "prepared_hash", "commitment_hash", "correctness_passed",
			"target_count", "opcode_counts", "declared_gas", "charged_gas",
		} {
			assert.Contains(t, top, key)
		}
		// error is optional and absent when execution succeeded.
		assert.NotContains(t, top, "error")
	})
}

func TestDecodeRejectsInvalidNumbersAndShapes(t *testing.T) {
	t.Run("negative repetition", func(t *testing.T) {
		var req Request
		err := json.Unmarshal([]byte(`{
			"schema_version": 2, "workload_path": "w.json", "session_id": "s",
			"mode": "diagnostic",
			"samples": [{"sample_id": "a", "case_id": "b", "repetition": -1, "phase": "diagnostic"}]
		}`), &req)
		assert.Error(t, err)
	})

	t.Run("fractional duration", func(t *testing.T) {
		var res Result
		err := json.Unmarshal([]byte(`{
			"schema_version": 2, "session_id": "s", "sample_id": "a", "case_id": "b",
			"repetition": 0, "phase": "diagnostic", "status": "executed",
			"execution_duration_ns": 1.5, "execution_boundary": "evm2_transaction_execution",
			"correctness_passed": true
		}`), &res)
		assert.Error(t, err)
	})

	t.Run("non-finite duration literal", func(t *testing.T) {
		var res Result
		err := json.Unmarshal([]byte(`{
			"schema_version": 2, "session_id": "s", "sample_id": "a", "case_id": "b",
			"repetition": 0, "phase": "diagnostic", "status": "executed",
			"execution_duration_ns": NaN, "execution_boundary": "evm2_transaction_execution",
			"correctness_passed": true
		}`), &res)
		assert.Error(t, err)
	})

	t.Run("negative seed", func(t *testing.T) {
		var w Workload
		err := json.Unmarshal([]byte(`{
			"schema_version": 2, "fork": "Osaka",
			"generator": {"revision": "r", "seed": -5},
			"cases": []
		}`), &w)
		require.NoError(t, err)
		assert.Error(t, w.Validate())
	})

	t.Run("parameters not an object", func(t *testing.T) {
		var w Workload
		err := json.Unmarshal([]byte(`{
			"schema_version": 2, "fork": "Osaka",
			"generator": {"revision": "r", "seed": 1},
			"cases": [{"id": "c", "status": "ready", "parameters": [1, 2]}]
		}`), &w)
		assert.Error(t, err)
	})
}
