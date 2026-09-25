"""Input loaders for runtimes CSV, opcounts JSON, and the fixture-name parser."""

from __future__ import annotations

import logging
from typing import NamedTuple

logger = logging.getLogger("evm_gasfit")


class FixtureMatchResult(NamedTuple):
    """The outcome of matching fixtures across the two inputs."""

    matched: set[str]
    only_runtimes: list[str]
    only_opcounts: list[str]


def report_unmatched_fixtures(
    runtimes_fixtures: set[str],
    opcounts_fixtures: set[str],
) -> FixtureMatchResult:
    """Warn about fixtures present in only one input and return the match outcome.

    Emits at most two warnings (one per direction) on the ``evm_gasfit`` logger,
    each a single line carrying only a count — the full list is exported via
    ``meta.json``.
    """
    only_runtimes = sorted(runtimes_fixtures - opcounts_fixtures)
    only_opcounts = sorted(opcounts_fixtures - runtimes_fixtures)
    if only_runtimes:
        logger.warning(
            "%d fixtures present in runtimes CSV but missing from opcounts JSON; "
            "dropped from analysis (see meta.json for full list)",
            len(only_runtimes),
        )
    if only_opcounts:
        logger.warning(
            "%d fixtures present in opcounts JSON but missing from runtimes CSV; "
            "dropped from analysis (see meta.json for full list)",
            len(only_opcounts),
        )
    return FixtureMatchResult(
        matched=runtimes_fixtures & opcounts_fixtures,
        only_runtimes=only_runtimes,
        only_opcounts=only_opcounts,
    )
