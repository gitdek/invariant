"""Invariant's runtime stand-in for nagini_contracts.

Nagini reads contracts statically; its own module isn't meant to run. When
the gate runs a Nagini-proved Python core (for its tests and its conformance
driver), it puts this module first on PYTHONPATH, so every contract call is a
no-op and the proved file runs unchanged. An application that proves a
function in place ships this package at its root, so the module that holds
the function imports wherever the application runs (#359).

Python evaluates a contract call's argument before the call, so Result()
returns an inert value: comparing it, doing arithmetic with it, calling it or
reading from it gives itself back, and it's true, so a contract such as
Ensures(Result() >= count) evaluates without effect.
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
class _Inert:
    """What Result() stands for when the program runs: nothing a contract
    does with it fails, and nothing it gives back means anything."""

    def __bool__(self) -> bool: return True
    def __len__(self) -> int: return 0
    def __hash__(self) -> int: return 0
    def __iter__(self) -> Any: return iter(())
    def __contains__(self, item: Any) -> bool: return True
    def __getattr__(self, name: str) -> Any: return self
    def __getitem__(self, key: Any) -> Any: return self
    def __call__(self, *args: Any, **kwargs: Any) -> Any: return self
    def _same(self, *args: Any) -> Any: return self
    __eq__ = __ne__ = __lt__ = __le__ = __gt__ = __ge__ = _same  # type: ignore
    __add__ = __radd__ = __sub__ = __rsub__ = __mul__ = __rmul__ = _same
    __truediv__ = __rtruediv__ = __floordiv__ = __rfloordiv__ = __mod__ = __rmod__ = _same
    __pow__ = __rpow__ = __and__ = __rand__ = __or__ = __ror__ = __xor__ = __rxor__ = _same
    __lshift__ = __rlshift__ = __rshift__ = __rrshift__ = __matmul__ = __rmatmul__ = _same
    __neg__ = __pos__ = __abs__ = __invert__ = _same


_INERT = _Inert()


def Result() -> Any: return _INERT
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
