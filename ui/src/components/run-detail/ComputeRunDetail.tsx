import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, FileText, LoaderCircle, ShieldCheck, XCircle } from 'lucide-react'
import { fetchData, fetchText } from '@/api/client'
import type { ComputeAnalysisArtifact, ComputeManifest, ComputeRunSummary, ComputeSample, RunConfig } from '@/api/types'
import { ErrorState } from '@/components/shared/ErrorState'
import { LoadingState } from '@/components/shared/Spinner'
import { formatNumber } from '@/utils/format'

interface ComputeRunDetailProps {
  runId: string
  compute: ComputeRunSummary
  metadata?: RunConfig['metadata']
}

interface SampleCounts {
  total: number
  executed: number
  failed: number
  unsupported: number
  correctnessPassed: number
  correctnessFailed: number
  qualification: number
  malformed: number
}

function isSafeRelativePath(path: string | undefined): path is string {
  if (!path || path.startsWith('/') || path.includes('\\')) return false
  return path.split('/').every((segment) => segment.length > 0 && segment !== '.' && segment !== '..')
}


function summarizeSamples(contents: string): SampleCounts {
  const counts: SampleCounts = {
    total: 0,
    executed: 0,
    failed: 0,
    unsupported: 0,
    correctnessPassed: 0,
    correctnessFailed: 0,
    qualification: 0,
    malformed: 0,
  }

  for (const line of contents.split('\n')) {
    if (!line.trim()) continue
    try {
      const sample = JSON.parse(line) as ComputeSample
      if (sample.status !== 'executed' && sample.status !== 'failed' && sample.status !== 'unsupported') {
        counts.malformed++
        continue
      }
      counts.total++
      counts[sample.status]++
      if (sample.correctness_passed === true) counts.correctnessPassed++
      if (sample.status === 'failed' && sample.correctness_passed === false) counts.correctnessFailed++
      if (sample.phase === 'qualification') counts.qualification++
    } catch {
      counts.malformed++
    }
  }

  return counts
}

function artifactLabel(artifact: ComputeAnalysisArtifact): string {
  if (artifact.name === 'analyzer.log') return 'Analyzer log'
  const prefix = artifact.path.includes('/reports/') ? 'Report: ' : ''
  return `${prefix}${artifact.name}`
}

function findQualificationArtifact(artifacts: ComputeAnalysisArtifact[]): ComputeAnalysisArtifact | undefined {
  return artifacts.find((artifact) => artifact.path.endsWith('/reports/qualification.csv'))
    ?? artifacts.find((artifact) => artifact.path.endsWith('/reports/new_gas_proposal.md'))
    ?? artifacts.find((artifact) => artifact.path.endsWith('/reports/analysis_status.json'))
}

function recordedText(record: Record<string, unknown> | undefined, key: string): string | undefined {
  const value = record?.[key]
  return typeof value === 'string' && value.length > 0 ? value : undefined
}

function isAwsCalibration(manifest: ComputeManifest | undefined, labels: Record<string, string> | undefined): boolean {
  const hardware = manifest?.hardware
  const provider = recordedText(hardware, 'provider')
  const instanceType = recordedText(hardware, 'instance_type')
  const calibration = recordedText(hardware, 'calibration_baseline') ?? labels?.calibration_baseline
  return calibration === 'aws-r7a.4xlarge' || (provider === 'aws' && instanceType === 'r7a.4xlarge')
}

function AnalysisStatusBadge({ status }: { status: ComputeRunSummary['analysis']['status'] }) {
  const styles = {
    pending: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200',
    running: 'bg-blue-100 text-blue-800 dark:bg-blue-900/50 dark:text-blue-200',
    succeeded: 'bg-green-100 text-green-800 dark:bg-green-900/50 dark:text-green-200',
    failed: 'bg-red-100 text-red-800 dark:bg-red-900/50 dark:text-red-200',
    inconclusive: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900/50 dark:text-yellow-200',
  }
  const Icon = status === 'succeeded' ? CheckCircle2 : status === 'running' ? LoaderCircle : status === 'failed' ? XCircle : AlertTriangle

  return (
    <span className={`inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs/5 font-medium ${styles[status]}`}>
      <Icon className={`size-3.5 ${status === 'running' ? 'animate-spin' : ''}`} />
      {status}
    </span>
  )
}

function ArtifactLink({ runId, path, children }: { runId: string; path: string; children: React.ReactNode }) {
  if (!isSafeRelativePath(path)) {
    return <span className="text-red-600 dark:text-red-400">Invalid artifact path</span>
  }

  return (
    <Link
      to="/runs/$runId/fileviewer"
      params={{ runId }}
      search={{ file: path }}
      target="_blank"
      className="break-all text-blue-600 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
    >
      {children}
    </Link>
  )
}

export function ComputeRunDetail({ runId, compute, metadata }: ComputeRunDetailProps) {
  const manifestPath = isSafeRelativePath(compute.manifest_path) ? `runs/${runId}/${compute.manifest_path}` : undefined
  const samplesPath = isSafeRelativePath(compute.samples_path) ? `runs/${runId}/${compute.samples_path}` : undefined
  const { data: manifest, isLoading: manifestLoading, error: manifestError, refetch: refetchManifest } = useQuery({
    queryKey: ['run', runId, 'compute-manifest', compute.manifest_path],
    queryFn: async () => {
      const { data, status } = await fetchData<ComputeManifest>(manifestPath!)
      if (!data) throw new Error(`Failed to fetch compute manifest: ${status}`)
      return data
    },
    enabled: !!manifestPath,
    retry: false,
  })
  const { data: sampleCounts, isLoading: samplesLoading, error: samplesError, refetch: refetchSamples } = useQuery({
    queryKey: ['run', runId, 'compute-samples', compute.samples_path],
    queryFn: async () => {
      const { data, status } = await fetchText(samplesPath!, { cacheBust: false })
      if (data === null) throw new Error(`Failed to fetch raw samples: ${status}`)
      return summarizeSamples(data)
    },
    enabled: !!samplesPath,
    retry: false,
  })

  const artifacts = compute.analysis.artifacts ?? []
  const analysisStatusPath = compute.analysis.attempt_id
    ? `analysis/${compute.analysis.attempt_id}/status.json`
    : undefined
  const qualificationArtifact = findQualificationArtifact(artifacts)
  const calibration = isAwsCalibration(manifest, metadata?.labels)
  const hardware = manifest?.hardware

  return (
    <div className="flex flex-col gap-6">
      <div className="overflow-hidden rounded-sm bg-white shadow-xs dark:bg-gray-800">
        <div className="flex flex-wrap items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-gray-700">
          <ShieldCheck className="size-4 text-gray-500 dark:text-gray-400" />
          <h2 className="text-sm/6 font-medium text-gray-900 dark:text-gray-100">Native compute campaign</h2>
          <span className="ml-auto font-mono text-xs/5 text-gray-500 dark:text-gray-400">
            {manifest?.boundary ?? 'Execution boundary recorded in manifest'}
          </span>
        </div>
        <div className="grid gap-4 p-4 sm:grid-cols-2">
          <div>
            <p className="text-xs/5 font-medium text-gray-500 dark:text-gray-400">Measurement purpose</p>
            <p className="mt-1 text-sm/6 text-gray-900 dark:text-gray-100">
              Local native execution samples verify correctness and preserve every terminal outcome.
            </p>
          </div>
          <div>
            <p className="text-xs/5 font-medium text-gray-500 dark:text-gray-400">Calibration status</p>
            <p className="mt-1 text-sm/6 text-gray-900 dark:text-gray-100">
              {calibration
                ? 'Recorded AWS r7a.4xlarge calibration host.'
                : 'Not an AWS r7a.4xlarge calibration unless that host is explicitly recorded in the manifest.'}
            </p>
          </div>
          {hardware && (
            <div className="sm:col-span-2">
              <p className="text-xs/5 font-medium text-gray-500 dark:text-gray-400">Recorded host facts</p>
              <p className="mt-1 font-mono text-xs/5 text-gray-700 dark:text-gray-300">
                {[
                  recordedText(hardware, 'hostname'),
                  recordedText(hardware, 'platform'),
                  recordedText(hardware, 'kernel_version'),
                  recordedText(hardware, 'cpu_model'),
                ].filter(Boolean).join(' · ') || 'No host facts were recorded.'}
              </p>
            </div>
          )}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="overflow-hidden rounded-sm bg-white shadow-xs dark:bg-gray-800">
          <div className="flex items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-gray-700">
            <FileText className="size-4 text-gray-500 dark:text-gray-400" />
            <h3 className="text-sm/6 font-medium text-gray-900 dark:text-gray-100">Measurement ledger</h3>
          </div>
          {!samplesPath ? (
            <ErrorState title="Invalid samples path" message="The compute summary does not contain a safe relative samples path." />
          ) : samplesLoading ? (
            <LoadingState message="Loading sample outcomes..." />
          ) : samplesError ? (
            <ErrorState title="Sample outcomes unavailable" message={samplesError.message} retry={() => refetchSamples()} />
          ) : sampleCounts && (
            <div className="grid grid-cols-2 gap-4 p-4 sm:grid-cols-3">
              <Stat label="Terminal samples" value={sampleCounts.total} />
              <Stat label="Executed" value={sampleCounts.executed} tone="green" />
              <Stat label="Failed" value={sampleCounts.failed} tone="red" />
              <Stat label="Unsupported" value={sampleCounts.unsupported} tone="yellow" />
              <Stat label="Correctness passed" value={sampleCounts.correctnessPassed} tone="green" />
              <Stat label="Qualification rows" value={sampleCounts.qualification} />
              {sampleCounts.correctnessFailed > 0 && <Stat label="Correctness not passed" value={sampleCounts.correctnessFailed} tone="red" />}
              {sampleCounts.malformed > 0 && <Stat label="Unreadable rows" value={sampleCounts.malformed} tone="red" />}
            </div>
          )}
          {samplesPath && (
            <div className="border-t border-gray-200 px-4 py-3 text-xs/5 dark:border-gray-700">
              <ArtifactLink runId={runId} path={compute.samples_path}>View raw samples.jsonl</ArtifactLink>
            </div>
          )}
        </div>

        <div className="overflow-hidden rounded-sm bg-white shadow-xs dark:bg-gray-800">
          <div className="flex flex-wrap items-center gap-2 border-b border-gray-200 px-4 py-3 dark:border-gray-700">
            <FileText className="size-4 text-gray-500 dark:text-gray-400" />
            <h3 className="text-sm/6 font-medium text-gray-900 dark:text-gray-100">evm-gasfit analysis</h3>
            <AnalysisStatusBadge status={compute.analysis.status} />
          </div>
          <div className="p-4 text-sm/6 text-gray-700 dark:text-gray-300">
            {compute.analysis.status === 'succeeded' ? (
              <p>All planned models qualified. Inspect the archived reports before using any result.</p>
            ) : compute.analysis.status === 'inconclusive' ? (
              <p>At least one planned model did not qualify. This run does not recommend prices; inspect qualification evidence and diagnostics.</p>
            ) : compute.analysis.status === 'failed' ? (
              <p>Analysis failed. Measurements remain available; inspect the attempt status and any archived diagnostics.</p>
            ) : compute.analysis.status === 'running' ? (
              <p>Analysis is still running. Measurement artifacts are already available for inspection.</p>
            ) : (
              <p>Analysis is pending. Measurement artifacts are already available for inspection.</p>
            )}
          </div>
          <div className="flex flex-col gap-2 border-t border-gray-200 px-4 py-3 text-xs/5 dark:border-gray-700">
            {analysisStatusPath && (
              <ArtifactLink runId={runId} path={analysisStatusPath}>View analysis status and error details</ArtifactLink>
            )}
            {qualificationArtifact ? (
              <ArtifactLink runId={runId} path={qualificationArtifact.path}>
                View qualification report ({qualificationArtifact.name})
              </ArtifactLink>
            ) : compute.analysis.attempt_id && (
              <span className="text-gray-500 dark:text-gray-400">No qualification report was produced; inspect the attempt status.</span>
            )}
            {artifacts
              .filter((artifact) => artifact.path !== qualificationArtifact?.path)
              .map((artifact) => (
                <ArtifactLink key={artifact.path} runId={runId} path={artifact.path}>{artifactLabel(artifact)}</ArtifactLink>
              ))}
            {artifacts.length === 0 && !compute.analysis.attempt_id && (
              <span className="text-gray-500 dark:text-gray-400">No analysis attempt artifacts are available yet.</span>
            )}
          </div>
        </div>
      </div>

      <div className="flex flex-wrap gap-x-4 gap-y-2 text-xs/5">
        {manifestPath ? (
          <ArtifactLink runId={runId} path={compute.manifest_path}>View manifest.json</ArtifactLink>
        ) : (
          <span className="text-red-600 dark:text-red-400">Invalid manifest path in compute summary.</span>
        )}
      </div>

      {manifestError && (
        <ErrorState title="Manifest unavailable" message={manifestError.message} retry={() => refetchManifest()} />
      )}
      {manifestLoading && <LoadingState message="Loading compute provenance..." />}
    </div>
  )
}

function Stat({ label, value, tone = 'default' }: { label: string; value: number; tone?: 'default' | 'green' | 'red' | 'yellow' }) {
  const tones = {
    default: 'text-gray-900 dark:text-gray-100',
    green: 'text-green-600 dark:text-green-400',
    red: 'text-red-600 dark:text-red-400',
    yellow: 'text-yellow-600 dark:text-yellow-400',
  }

  return (
    <div>
      <p className="text-xs/5 text-gray-500 dark:text-gray-400">{label}</p>
      <p className={`mt-1 text-xl/7 font-semibold ${tones[tone]}`}>{formatNumber(value)}</p>
    </div>
  )
}
