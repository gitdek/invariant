"""Invariant's runtime stand-in for nagini_contracts.

Nagini reads contracts statically; its own module isn't meant to run. When
the gate runs a Nagini-proved Python core (for its tests and its conformance
driver), it puts this module first on PYTHONPATH, so every contract call is a
no-op and the proved file runs unchanged. An application that proves a
function in place ships this package at its root, so the module that holds
the function imports wherever the application runs (#359).

Python evaluates a contract call's arguments before the call, so Result()
returns an inert value: comparing it, doing arithmetic with it, calling it or
reading from it gives itself back, and it's true. It's made afresh, class and
all, on every call, so nothing a program does to one reaches another. What
else a contract may hold, so that evaluating it can't fail where Nagini reads
it lazily, the gate checks in the function's source.
"""

from typing import Any, Callable, TypeVar

T = TypeVar("T")

__all__ = (
    "Requires", "Ensures", "Invariant", "Assert", "Assume", "Exsures", "Decreases",
    "Acc", "Rd", "Wildcard", "list_pred", "dict_pred", "set_pred", "Implies", "Old",
    "Result", "Forall", "Exists", "Pure", "Predicate", "Fold", "Unfold", "Unfolding",
    "Ghost", "ContractOnly", "Import", "MayCreate", "Low", "LowVal", "Let",
)


def Requires(expr: Any) -> None: pass
def Ensures(expr: Any) -> None: pass
def Invariant(expr: Any) -> None: pass
def Assert(expr: Any) -> None: pass
def Assume(expr: Any) -> None: pass
def Exsures(exception: Any, expr: Any) -> None: pass
def Decreases(*args: Any) -> None: pass
def Acc(*args: Any, **kwargs: Any) -> bool: return True
def Rd(*args: Any) -> bool: return True
def Wildcard(*args: Any) -> bool: return True
def list_pred(*args: Any) -> bool: return True
def dict_pred(*args: Any) -> bool: return True
def set_pred(*args: Any) -> bool: return True
def Implies(p: Any, q: Any) -> bool: return True
def Old(x: T) -> T: return x


def _same(self: Any, *args: Any, **kwargs: Any) -> Any: return self


# The operators Result()'s value answers with itself, reflected forms and all.
_OPERATORS = (
    "__eq__", "__ne__", "__lt__", "__le__", "__gt__", "__ge__",
    "__add__", "__radd__", "__sub__", "__rsub__", "__mul__", "__rmul__",
    "__truediv__", "__rtruediv__", "__floordiv__", "__rfloordiv__", "__mod__", "__rmod__",
    "__pow__", "__rpow__", "__and__", "__rand__", "__or__", "__ror__", "__xor__", "__rxor__",
    "__lshift__", "__rlshift__", "__rshift__", "__rrshift__", "__matmul__", "__rmatmul__",
    "__neg__", "__pos__", "__abs__", "__invert__", "__getattr__", "__getitem__", "__call__",
)


def Result() -> Any:
    inert = dict.fromkeys(_OPERATORS, _same)
    inert.update({
        "__slots__": (), "__bool__": lambda self: True, "__len__": lambda self: 0, "__hash__": lambda self: 0,
        "__iter__": lambda self: iter(()), "__contains__": lambda self, item: True,
    })
    return type("Result", (), inert)()


def Forall(*args: Any) -> bool: return True
def Exists(*args: Any) -> bool: return True
def Pure(f: T) -> T: return f
def Predicate(f: T) -> T: return f
def Fold(*args: Any) -> None: pass
def Unfold(*args: Any) -> None: pass
def Unfolding(pred: Any, expr: T) -> T: return expr
def Ghost(f: T) -> T: return f
def ContractOnly(f: T) -> T: return f
def Import(*args: Any) -> None: pass
def MayCreate(*args: Any) -> bool: return True
def Low(*args: Any) -> bool: return True
def LowVal(*args: Any) -> bool: return True
def Let(*args: Any) -> Any: return True
