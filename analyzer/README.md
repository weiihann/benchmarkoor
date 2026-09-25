# evm-gasfit (benchmarkoor analyzer)

`evm-gasfit` fits EVM runtime measurements to gas costs. Benchmarkoor's compute
campaigns use it as their analyzer: it fits `runtime = intercept + slope × count`
per workload variant with NNLS, estimates uncertainty with a session-clustered
bootstrap, applies the qualification gates, subtracts calibrated glue cost, and
writes CSV and Markdown reports.

The package was imported from [evm-gasfit](https://github.com/misilva73/evm-gasfit)
by Maria Silva, released under CC0 1.0, at the `weiihann/evm-gasfit` fork's
`6f0eecc` plus that fork's uncommitted `recommendations.py` changes. It now lives
here under benchmarkoor's license. The EEST and zkEVM input adapters and the docs
site were not imported; benchmarkoor produces the analyzer inputs itself.

## How benchmarkoor uses it

- `Dockerfile.compute-analyzer` builds this directory into the analyzer image,
  whose entrypoint is `evm-gasfit`.
- `pkg/compute/analysis.go` runs `evm-gasfit run --config --runtimes --opcounts
  --manifest --out` over the inputs `pkg/compute/export.go` writes.
- `scripts/compute/pricing_campaign.py` and campaign drivers run
  `python -m evm_gasfit.recommendations create-config` and `build` in the same image.
- `evm-gasfit compare-campaigns --baseline --candidate --out` compares two
  analysis directories.

## Develop

```bash
cd analyzer
uv sync
uv run pytest
uv run ruff check . && uv run ruff format --check .
```

`uv.lock` pins every analysis dependency; the image builds with `uv sync --locked`.
