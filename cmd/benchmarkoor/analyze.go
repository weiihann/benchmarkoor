package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	computeAnalyzeRunDir         string
	computeAnalyzeConfigOverride string
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze --run <dir>",
	Short: "Analyze an archived compute campaign",
	Long: `Create a new immutable evm-gasfit analysis attempt from an archived compute campaign.

The command uses the exact analysis configuration archived with the run unless
--analysis-config supplies an explicit replacement. It does not execute a
benchmark worker or regenerate workloads.`,
	RunE: runAnalyze,
}

func init() {
	rootCmd.AddCommand(analyzeCmd)
	analyzeCmd.Flags().StringVar(&computeAnalyzeRunDir, "run", "", "Archived compute run directory")
	analyzeCmd.Flags().StringVar(
		&computeAnalyzeConfigOverride,
		"analysis-config",
		"",
		"Override the analysis YAML archived with the run",
	)
	if err := analyzeCmd.MarkFlagRequired("run"); err != nil {
		panic(err)
	}
}

func runAnalyze(_ *cobra.Command, _ []string) error {
	if computeAnalyzeRunDir == "" {
		return fmt.Errorf("--run is required")
	}

	return runComputeAnalysis(computeAnalyzeRunDir, computeAnalyzeConfigOverride)
}
