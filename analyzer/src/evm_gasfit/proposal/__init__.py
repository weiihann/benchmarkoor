"""Proposal aggregation, derived-formula evaluation, and assembly.

Only :mod:`derived` is exported eagerly — :mod:`config` imports from it during
its own initialization, so the aggregate / build modules must be imported
lazily via ``evm_gasfit.proposal.aggregate`` / ``.build``.
"""

from __future__ import annotations

from .derived import evaluate, names_referenced, parse_formula

__all__ = ["evaluate", "names_referenced", "parse_formula"]
