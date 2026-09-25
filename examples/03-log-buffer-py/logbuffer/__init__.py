"""A bounded buffer for log shipping."""
from logbuffer.core import State, done, ship, ship_fail, write

__all__ = ["State", "write", "ship", "ship_fail", "done"]
