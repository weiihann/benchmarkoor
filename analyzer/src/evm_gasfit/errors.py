"""Custom exception types raised by ``evm_gasfit``."""

from __future__ import annotations


class ConfigError(ValueError):
    """Raised for hard configuration and input errors; CLI maps it to exit 1."""


class ModelingError(ValueError):
    """Raised when every fit is skipped and no rows are produced; CLI maps it to exit 2."""
