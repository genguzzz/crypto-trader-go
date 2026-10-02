"""Resource limits a strategy process applies to itself before running user code."""

from __future__ import annotations

import os
import signal
import sys


def apply(parent_death: bool = False) -> None:
    """Cap address space (CTG_MEM_LIMIT_MB, default 256) and CPU time (CTG_CPU_LIMIT_S, default 6 h)."""
    try:
        import resource
    except ImportError:  # not POSIX
        return
    mb = int(os.environ.get("CTG_MEM_LIMIT_MB", "256"))
    if mb > 0:
        _cap(resource, resource.RLIMIT_AS, mb << 20)
    cpu = int(os.environ.get("CTG_CPU_LIMIT_S", str(6 * 3600)))
    if cpu > 0:
        _cap(resource, resource.RLIMIT_CPU, cpu)
    if parent_death and sys.platform.startswith("linux"):
        try:
            import ctypes

            ctypes.CDLL(None, use_errno=True).prctl(1, signal.SIGKILL)  # PR_SET_PDEATHSIG
        except (OSError, AttributeError):
            pass


def _cap(resource, which: int, value: int) -> None:
    _, hard = resource.getrlimit(which)
    if hard != resource.RLIM_INFINITY:
        value = min(value, hard)
    resource.setrlimit(which, (value, value))
