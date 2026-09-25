"""Safe arithmetic mini-language for ``derived:`` entries in user config.

Parses a small whitelisted subset of Python expressions, lists referenced
identifiers, and evaluates against an environment of numeric values. The
same AST walker is used for both load-time validation and runtime evaluation.
"""

from __future__ import annotations

import ast
from collections.abc import Mapping

from evm_gasfit.errors import ConfigError

_BIN_OPS: dict[type[ast.operator], object] = {
    ast.Add: lambda a, b: a + b,
    ast.Sub: lambda a, b: a - b,
    ast.Mult: lambda a, b: a * b,
    ast.Div: lambda a, b: a / b,
    ast.FloorDiv: lambda a, b: a // b,
}

_UNARY_OPS: dict[type[ast.unaryop], object] = {
    ast.UAdd: lambda a: +a,
    ast.USub: lambda a: -a,
}

# Variadic functions callable from a formula. ``max``/``min`` enable clamping
# (``max(0, x)``) and worst-across-candidates combines.
_FUNCS: dict[str, object] = {"max": max, "min": min}


def _reject(node: ast.AST, formula: str | None = None) -> None:
    suffix = f" in formula {formula!r}" if formula is not None else ""
    raise ConfigError(f"unsupported node {type(node).__name__}{suffix}")


def _validate(node: ast.AST, formula: str) -> None:
    if isinstance(node, ast.Expression):
        _validate(node.body, formula)
    elif isinstance(node, ast.Constant):
        if not isinstance(node.value, (int, float)) or isinstance(node.value, bool):
            raise ConfigError(
                f"unsupported constant {node.value!r} in formula {formula!r}"
            )
    elif isinstance(node, ast.Name):
        if not isinstance(node.ctx, ast.Load):
            _reject(node, formula)
    elif isinstance(node, ast.BinOp):
        if type(node.op) not in _BIN_OPS:
            _reject(node.op, formula)
        _validate(node.left, formula)
        _validate(node.right, formula)
    elif isinstance(node, ast.UnaryOp):
        if type(node.op) not in _UNARY_OPS:
            _reject(node.op, formula)
        _validate(node.operand, formula)
    elif isinstance(node, ast.Call):
        if not isinstance(node.func, ast.Name) or node.func.id not in _FUNCS:
            _reject(node, formula)
        if node.keywords or not node.args:
            raise ConfigError(
                f"{node.func.id if isinstance(node.func, ast.Name) else 'call'}() "
                f"takes at least one positional argument and no keywords "
                f"in formula {formula!r}"
            )
        for arg in node.args:
            if isinstance(arg, ast.Starred):
                _reject(arg, formula)
            _validate(arg, formula)
    else:
        _reject(node, formula)


def parse_formula(formula: str) -> ast.Expression:
    try:
        tree = ast.parse(formula, mode="eval")
    except SyntaxError as exc:
        raise ConfigError(f"invalid syntax in formula {formula!r}: {exc.msg}") from exc
    _validate(tree, formula)
    return tree


def names_referenced(tree: ast.Expression) -> list[str]:
    out: list[str] = []

    def visit(node: ast.AST) -> None:
        if isinstance(node, ast.Name):
            out.append(node.id)
        elif isinstance(node, ast.Expression):
            visit(node.body)
        elif isinstance(node, ast.BinOp):
            visit(node.left)
            visit(node.right)
        elif isinstance(node, ast.UnaryOp):
            visit(node.operand)
        elif isinstance(node, ast.Call):
            for arg in node.args:
                visit(arg)

    visit(tree)
    return out


def _eval(node: ast.AST, env: Mapping[str, int | float | None]) -> float | None:
    if isinstance(node, ast.Expression):
        return _eval(node.body, env)
    if isinstance(node, ast.Constant):
        return node.value
    if isinstance(node, ast.Name):
        if node.id not in env:
            raise ConfigError(f"unknown identifier {node.id!r} in formula")
        return env[node.id]
    if isinstance(node, ast.BinOp):
        left = _eval(node.left, env)
        right = _eval(node.right, env)
        if left is None or right is None:
            return None
        if isinstance(node.op, (ast.Div, ast.FloorDiv)) and right == 0:
            raise ConfigError("division by zero in formula")
        return _BIN_OPS[type(node.op)](left, right)
    if isinstance(node, ast.UnaryOp):
        operand = _eval(node.operand, env)
        if operand is None:
            return None
        return _UNARY_OPS[type(node.op)](operand)
    if isinstance(node, ast.Call):
        args = [_eval(arg, env) for arg in node.args]
        if any(a is None for a in args):
            return None
        return _FUNCS[node.func.id](*args)
    _reject(node)


def evaluate(
    tree: ast.Expression, env: Mapping[str, int | float | None]
) -> float | None:
    result = _eval(tree, env)
    return None if result is None else float(result)
