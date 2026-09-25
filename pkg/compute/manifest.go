package compute

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/ethpandaops/benchmarkoor/pkg/config"
	"github.com/ethpandaops/benchmarkoor/pkg/docker"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

const computeManifestSchemaVersion = 1

type computeManifest struct {
	SchemaVersion     int                    `json:"schema_version"`
	CreatedAt         time.Time              `json:"created_at"`
	Software          map[string]any         `json:"software"`
	Hardware          map[string]any         `json:"hardware"`
	Workloads         map[string]any         `json:"workloads"`
	ComparisonFactors map[string]any         `json:"comparison_factors"`
	Boundary          string                 `json:"boundary"`
	GasSchedule       map[string]any         `json:"gas_schedule"`
	Phase             map[string]any         `json:"phase"`
	Ordering          map[string]any         `json:"ordering"`
	Policy            map[string]any         `json:"policy"`
	HostProvenance    map[string]any         `json:"host_provenance"`
	Provenance        map[string]sourceFacts `json:"provenance"`
}

type sourceFacts struct {
	Path          string            `json:"path,omitempty"`
	Available     bool              `json:"available"`
	Unavailable   string            `json:"unavailable,omitempty"`
	Revision      string            `json:"revision,omitempty"`
	Dirty         *bool             `json:"dirty,omitempty"`
	DirtyPatchSHA string            `json:"dirty_patch_sha256,omitempty"`
	LockHashes    map[string]string `json:"lock_hashes,omitempty"`
}

func buildComputeManifest(
	ctx context.Context,
	cfg *config.ComputeConfig,
	workloadPath, workloadSHA string,
	images map[string]string,
	selection map[string]any,
	phasePlan map[string]any,
	resourceLimits *docker.ResourceLimits,
) computeManifest {
	hardware, hostProvenance := hostFacts(resourceLimits)
	// cfg.Validate has already restricted Engine to a known value.
	boundary := engineBoundaries[cfg.Engine]
	workloads := map[string]any{
		"sha256":             workloadSHA,
		"original_selection": selection,
		"resolved_selection": selection,
		"fixed_limits":       computeFixedLimits(cfg),
	}
	gasSchedule := map[string]any{
		"fork":                  SupportedFork,
		"transaction_gas_limit": maxOsakaTransactionGas,
		"active_schedule_identity": map[string]string{
			"status": "unavailable",
			"reason": "the worker protocol does not expose a gas-schedule identity",
		},
	}
	return computeManifest{
		SchemaVersion: computeManifestSchemaVersion,
		CreatedAt:     time.Now().UTC(),
		Software: map[string]any{
			"engine":  cfg.Engine,
			"images":  images,
			"runtime": cfg.ContainerRuntime,
		},
		Hardware:  hardware,
		Workloads: workloads,
		ComparisonFactors: map[string]any{
			"hardware":           hardware,
			"workload":           workloads,
			"execution_boundary": boundary,
			"gas_schedule":       gasSchedule,
		},
		Boundary:    boundary,
		GasSchedule: gasSchedule,
		Phase:       phasePlan,
		Ordering:    map[string]any{"seed": cfg.Seed, "algorithm": "math/rand/v2 PCG Fisher-Yates"},
		Policy: map[string]any{
			"baseline":                 "fresh worker process and worker-restored baseline for every requested sample",
			"qualification_adaptation": false,
			"generator_config_path":    generatorConfigPath(cfg),
			"workload_artifact_path":   workloadPath,
		},
		HostProvenance: hostProvenance,
		Provenance:     collectSourceFacts(ctx, cfg.SourcePaths),
	}
}

const (
	maxOsakaTransactionGas   = uint64(1 << 24)
	defaultGeneratorTxGasCap = uint64(15_000_000)
)

func computeFixedLimits(cfg *config.ComputeConfig) map[string]any {
	limits := map[string]any{"osaka_tx_gas_cap": maxOsakaTransactionGas}
	// Pre-generated workloads do not declare the generator's configured cap.
	// Their transaction allowances remain available in the workload artifact.
	if cfg.Generator != nil {
		limits["generator_tx_gas_cap"] = generatorTxGasCap(cfg.Generator)
	}

	return limits
}

func generatorConfigPath(cfg *config.ComputeConfig) string {
	if cfg.Generator == nil {
		return ""
	}

	return "generator.json"
}

func hostFacts(resourceLimits *docker.ResourceLimits) (map[string]any, map[string]any) {
	hardware := map[string]any{
		"goos":   runtime.GOOS,
		"goarch": runtime.GOARCH,
	}
	hostProvenance := make(map[string]any)
	if resourceLimits != nil {
		hardware["resource_allocation"] = resourceLimits
	}
	if details, err := host.Info(); err == nil {
		hardware["hostname"] = details.Hostname
		hardware["os"] = details.OS
		hardware["platform"] = details.Platform
		hardware["platform_version"] = details.PlatformVersion
		hardware["kernel_version"] = details.KernelVersion
		hardware["virtualization"] = details.VirtualizationSystem
	} else {
		hardware["host_info_unavailable"] = err.Error()
	}
	if details, err := cpu.Info(); err == nil && len(details) > 0 {
		hardware["cpu_vendor"] = details[0].VendorID
		hardware["cpu_model"] = details[0].ModelName
		hardware["cpu_cache_kb"] = details[0].CacheSize
		hostProvenance["cpu_mhz"] = details[0].Mhz
	} else if err != nil {
		hardware["cpu_info_unavailable"] = err.Error()
		hostProvenance["cpu_mhz_unavailable"] = err.Error()
	} else {
		hostProvenance["cpu_mhz_unavailable"] = "cpu information was empty"
	}
	if count, err := cpu.Counts(false); err == nil {
		hardware["cpu_cores"] = count
	}
	if count, err := cpu.Counts(true); err == nil {
		hardware["cpu_threads"] = count
	}
	if details, err := mem.VirtualMemory(); err == nil {
		hardware["memory_total_bytes"] = details.Total
	} else {
		hardware["memory_info_unavailable"] = err.Error()
	}

	return hardware, hostProvenance
}

func collectSourceFacts(ctx context.Context, paths map[string]string) map[string]sourceFacts {
	names := []string{"benchmarkoor", "newl1", "execution_specs"}
	facts := make(map[string]sourceFacts, len(names))
	for _, name := range names {
		facts[name] = collectOneSourceFacts(ctx, paths[name])
	}

	return facts
}

func collectOneSourceFacts(ctx context.Context, root string) sourceFacts {
	facts := sourceFacts{Path: root, LockHashes: make(map[string]string)}
	if root == "" {
		facts.Unavailable = "source path was not configured"
		return facts
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		if err != nil {
			facts.Unavailable = fmt.Sprintf("source path is unavailable: %v", err)
		} else {
			facts.Unavailable = "source path is not a directory"
		}
		return facts
	}
	facts.Available = true

	if revision, err := commandOutput(ctx, root, "git", "rev-parse", "HEAD"); err == nil {
		facts.Revision = revision
	} else {
		facts.Unavailable = "git revision unavailable: " + err.Error()
	}
	if status, err := commandOutput(ctx, root, "git", "status", "--porcelain"); err == nil {
		dirty := status != ""
		facts.Dirty = &dirty
		if dirty {
			patch, patchErr := dirtyPatch(ctx, root, status)
			if patchErr != nil {
				facts.Unavailable = appendUnavailable(facts.Unavailable, "dirty patch unavailable: "+patchErr.Error())
			} else {
				facts.DirtyPatchSHA = sha256Bytes(patch)
			}
		}
	} else {
		facts.Unavailable = appendUnavailable(facts.Unavailable, "git dirty state unavailable: "+err.Error())
	}

	for _, name := range []string{"go.sum", "Cargo.lock", "uv.lock", "poetry.lock", "requirements.txt", "requirements.lock"} {
		path := filepath.Join(root, name)
		bytes, err := os.ReadFile(path)
		if err == nil {
			facts.LockHashes[name] = sha256Bytes(bytes)
		}
	}
	if len(facts.LockHashes) == 0 {
		facts.LockHashes = nil
	} else {
		sorted := make(map[string]string, len(facts.LockHashes))
		keys := make([]string, 0, len(facts.LockHashes))
		for name := range facts.LockHashes {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			sorted[name] = facts.LockHashes[name]
		}
		facts.LockHashes = sorted
	}

	return facts
}

func commandOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(output)), nil
}

func dirtyPatch(ctx context.Context, root, status string) ([]byte, error) {
	patch, err := commandOutput(ctx, root, "git", "diff", "--binary", "HEAD")
	if err != nil {
		return nil, err
	}
	var evidence bytes.Buffer
	evidence.WriteString(status)
	evidence.WriteByte('\n')
	evidence.WriteString(patch)
	untracked, err := commandOutput(ctx, root, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(untracked, "\n") {
		if name == "" {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, fmt.Errorf("reading untracked file %q: %w", name, err)
		}
		evidence.WriteString("\nuntracked ")
		evidence.WriteString(name)
		evidence.WriteByte('\n')
		evidence.WriteString(sha256Bytes(contents))
		evidence.WriteByte('\n')
	}

	return evidence.Bytes(), nil
}

func appendUnavailable(existing, next string) string {
	if existing == "" {
		return next
	}

	return existing + "; " + next
}

func sha256Text(value string) string {
	return sha256Bytes([]byte(value))
}

func sha256Bytes(value []byte) string {
	sum := sha256.Sum256(value)

	return hex.EncodeToString(sum[:])
}
