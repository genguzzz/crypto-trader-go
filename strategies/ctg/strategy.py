"""Strategy base class, parameters and dynamic universes."""

from __future__ import annotations

import re
from typing import Any


class Param:
    """A tunable numeric parameter. Read it as `self.<name>` inside handlers.

    An int default makes the value an int (e.g. indicator lengths)."""

    def __init__(self, default: float, min: float | None = None, max: float | None = None,
                 step: float | None = None, desc: str = "", ai_tunable: bool = False):
        self.default = default
        self.min, self.max, self.step = min, max, step
        self.desc = desc
        self.ai_tunable = ai_tunable
        self.name = ""

    def __set_name__(self, owner, name):
        self.name = name

    def __get__(self, obj, objtype=None):
        if obj is None:
            return self
        return obj._params.get(self.name, self.default)

    def coerce(self, v: Any):
        v = float(v)
        if self.min is not None:
            v = max(v, float(self.min))
        if self.max is not None:
            v = min(v, float(self.max))
        if isinstance(self.default, int) and not isinstance(self.default, bool):
            return int(round(v))
        return v

    def spec(self) -> dict:
        d: dict[str, Any] = {"name": self.name, "default": float(self.default)}
        for k in ("min", "max", "step"):
            if getattr(self, k) is not None:
                d[k] = float(getattr(self, k))
        if self.desc:
            d["desc"] = self.desc
        if self.ai_tunable:
            d["aiTunable"] = True
        return d


class Screener:
    """Dynamic universe: the top instruments of the pool by a rule's score, re-evaluated every `refresh`."""

    def __init__(self, rule: str = "", side: str = "long", top: int = 20, min_score: float | None = None,
                 filter: dict[str, tuple[float, float]] | None = None, refresh: str = "1H"):
        self.rule, self.side, self.top = rule, side, int(top)
        self.min_score, self.filter, self.refresh = min_score, filter or {}, refresh

    def to_wire(self) -> dict:
        d: dict[str, Any] = {"rule": self.rule, "side": self.side, "top": self.top, "refresh": self.refresh}
        if self.min_score is not None:
            d["minScore"] = float(self.min_score)
        if self.filter:
            d["filter"] = {k: [float(v[0]), float(v[1])] for k, v in self.filter.items()}
        return d


def _kebab(name: str) -> str:
    return re.sub(r"(?<!^)(?=[A-Z])", "-", name).lower()


class Strategy:
    """Subclass and override the handlers you need. Handlers may return a Signal, a list of Signals or None.

    Class attributes: name, description, universe (list of instIds or a Screener), timeframe
    (1m 5m 15m 1H 4H 1D) and Param fields."""

    name: str = ""
    description: str = ""
    universe: list[str] | Screener = []
    timeframe: str = "1H"

    def __init__(self, params: dict | None = None):
        self._params: dict[str, Any] = {}
        specs = self.param_specs()
        for k, v in (params or {}).items():
            p = specs.get(k)
            if p is not None and v is not None:
                self._params[k] = p.coerce(v)

    @classmethod
    def param_specs(cls) -> dict[str, Param]:
        out: dict[str, Param] = {}
        for klass in reversed(cls.__mro__):
            for k, v in vars(klass).items():
                if isinstance(v, Param):
                    out[k] = v
        return out

    @classmethod
    def strategy_name(cls) -> str:
        return cls.name or _kebab(cls.__name__)

    @classmethod
    def meta(cls) -> dict:
        u = cls.universe
        universe = {"screener": u.to_wire()} if isinstance(u, Screener) else {"static": list(u or [])}
        doc = (cls.__doc__ or "").strip()
        return {"class": cls.__name__, "name": cls.strategy_name(), "description": cls.description or doc,
                "timeframe": cls.timeframe, "params": [p.spec() for p in cls.param_specs().values()],
                "universe": universe}

    # ---- handlers ----
    def on_start(self, ctx):
        pass

    def on_bar(self, ctx, bar):
        pass

    def on_universe_change(self, ctx, added, removed):
        pass

    def on_fill(self, ctx, fill):
        pass

    def on_signal_result(self, ctx, result):
        pass

    def on_funding(self, ctx, inst, rate, amount):
        pass

    def on_stop(self, ctx):
        pass
