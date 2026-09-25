"""Invariant's runtime stand-in for nagini_contracts.

Nagini reads contracts statically; its own module isn't meant to run. When
the gate runs a Nagini-proved Python core (for its tests and its conformance
driver), it puts this module first on PYTHONPATH, so every contract call is a
no-op and the proved file runs unchanged.
"""

from typing import Any, Callable, TypeVar

T = TypeVar("T")

__all__ = [
    "Requires", "Ensures", "Invariant", "Assert", "Assume", "Exsures", "Decreases",
    "Acc", "Rd", "Wildcard", "list_pred", "dict_pred", "set_pred", "Implies", "Old",
    "Result", "Forall", "Exists", "Pure", "Predicate", "Fold", "Unfold", "Unfolding",
    "Ghost", "ContractOnly", "Import", "MayCreate", "Low", "LowVal", "Let",
]


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
def Result() -> Any: return None
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
