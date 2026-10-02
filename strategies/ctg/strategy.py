"""Strategy base class, typed parameters and dynamic universes."""

from __future__ import annotations

import math
import re
from typing import Any

TIMEFRAMES = ["1m", "5m", "15m", "1H", "4H", "1D"]
INTERVALS = TIMEFRAMES  # valid timer intervals (schedule)


class Param:
    """A numeric parameter (int when the default is an int). Read it as `self.<name>` in handlers.

    Every parameter type shares group (form section), label (display name), desc (help text) and
    ai_tunable (the AI layer may propose changes, applied only after the owner approves them).
    On the wire every value is a number; subclasses map it to their own type."""

    kind = ""  # "" = int or float by the default (SDK 1)

    def __init__(self, default: Any, min: float | None = None, max: float | None = None, step: float | None = None,
                 desc: str = "", ai_tunable: bool = False, group: str = "", label: str = ""):
        self.default = default
        self.min, self.max, self.step = min, max, step
        self.desc, self.ai_tunable, self.group, self.label = desc, ai_tunable, group, label
        self.name = ""

    def __set_name__(self, owner, name):
        self.name = name

    def __get__(self, obj, objtype=None):
        if obj is None:
            return self
        if self.name in obj._params:
            return self.decode(obj._params[self.name])
        return self.default

    @property
    def type(self) -> str:
        if self.kind:
            return self.kind
        return "int" if isinstance(self.default, int) and not isinstance(self.default, bool) else "float"

    def encode(self, v: Any) -> float:
        """Value → wire number."""
        return float(v)

    def decode(self, x: float) -> Any:
        """Wire number → value."""
        return int(round(x)) if self.type == "int" else float(x)

    def coerce(self, v: Any) -> float:
        """A wire number from the host, clamped into range (the host validates too)."""
        v = float(v)
        if self.min is not None:
            v = max(v, float(self.min))
        if self.max is not None:
            v = min(v, float(self.max))
        return float(round(v)) if self.type == "int" else v

    def problems(self) -> list[str]:
        """What is wrong with the declaration (checked by the editor)."""
        out = []
        d = self.encode(self.default)
        if not math.isfinite(d):
            out.append("默认值不是有效数字")
        if self.min is not None and self.max is not None and self.min > self.max:
            out.append(f"min {self.min} 大于 max {self.max}")
        if (self.min is not None and d < self.min) or (self.max is not None and d > self.max):
            out.append(f"默认值 {self.default} 超出范围 [{self.min}, {self.max}]")
        if self.type == "int" and d != int(d):
            out.append("Int 的默认值需为整数")
        return out

    def spec(self) -> dict:
        d: dict[str, Any] = {"name": self.name, "type": self.type, "default": self.encode(self.default)}
        for k in ("min", "max", "step"):
            if getattr(self, k) is not None:
                d[k] = float(getattr(self, k))
        for k, key in (("desc", "desc"), ("group", "group"), ("label", "label")):
            if getattr(self, k):
                d[key] = getattr(self, k)
        if self.ai_tunable:
            d["aiTunable"] = True
        return d


class Float(Param):
    """A decimal number: Float(2.0, min=0.5, max=6, step=0.25)."""

    kind = "float"

    def __init__(self, default: float, min: float | None = None, max: float | None = None, step: float | None = None, **kw):
        super().__init__(float(default), min, max, step, **kw)


class Int(Param):
    """A whole number: Int(20, min=5, max=100)."""

    kind = "int"

    def __init__(self, default: int, min: float | None = None, max: float | None = None, step: float | None = 1, **kw):
        super().__init__(int(default), min, max, step, **kw)


class Bool(Param):
    """A switch: Bool(True). Reads as True / False."""

    kind = "bool"

    def __init__(self, default: bool, **kw):
        super().__init__(bool(default), **kw)

    def encode(self, v: Any) -> float:
        return 1.0 if v else 0.0

    def decode(self, x: float) -> bool:
        return x >= 0.5

    def coerce(self, v: Any) -> float:
        return 1.0 if float(v) >= 0.5 else 0.0


class Choice(Param):
    """One of a fixed list of strings: Choice("both", ["long", "short", "both"]). Reads as the string.

    labels (optional) are the names shown in the form, one per option."""

    kind = "choice"

    def __init__(self, default: str, options: list[str], labels: list[str] | None = None, **kw):
        kw.pop("min", None), kw.pop("max", None), kw.pop("step", None)
        super().__init__(default, **kw)
        self.options = [str(o) for o in options]
        self.labels = [str(x) for x in labels] if labels else []

    def encode(self, v: Any) -> float:
        return float(self.options.index(v)) if v in self.options else math.nan

    def decode(self, x: float) -> str:
        i = int(round(x))
        return self.options[i] if 0 <= i < len(self.options) else self.default

    def coerce(self, v: Any) -> float:
        i = int(round(float(v)))
        return float(min(max(i, 0), len(self.options) - 1))

    def problems(self) -> list[str]:
        out = []
        if not self.options:
            out.append("options 为空")
        elif self.default not in self.options:
            out.append(f"默认值 {self.default!r} 不在 {self.options} 里")
        if len(set(self.options)) != len(self.options):
            out.append("options 有重复")
        if self.labels and len(self.labels) != len(self.options):
            out.append("labels 需和 options 一一对应")
        return out

    def spec(self) -> dict:
        d = super().spec()
        d["options"] = list(self.options)
        if self.labels:
            d["labels"] = list(self.labels)
        return d


class Timeframe(Choice):
    """The bar timeframe as a parameter, so each instance can pick it: timeframe = Timeframe("4H")."""

    kind = "timeframe"

    def __init__(self, default: str = "1H", options: list[str] | None = None, **kw):
        kw.setdefault("label", "K 线周期")
        super().__init__(default, options or TIMEFRAMES, **kw)

    def problems(self) -> list[str]:
        out = super().problems()
        bad = [o for o in self.options if o not in TIMEFRAMES]
        if bad:
            out.append(f"未知周期 {bad}（可选 {TIMEFRAMES}）")
        return out


class Screener:
    """Dynamic universe: the top instruments of the pool by a rule's score, re-evaluated every `refresh`.

    side: long | short | both (ranked by the stronger side)."""

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

    Class attributes:
      name         stable id, default the kebab-case class name
      description  one line (default: the docstring)
      universe     list of instIds, or a Screener
      timeframe    "1m" "5m" "15m" "1H" "4H" "1D", or Timeframe("4H") to let instances choose
      can_long / can_short   directions the strategy may open (shown on its card)
      warmup       bars of history the strategy reads (shown to the owner; ctx.bars fetches them)
      schedule     {"name": "15m"}: on_timer(ctx, "name") every 15 minutes
    and parameter fields (Int, Float, Bool, Choice, Timeframe)."""

    name: str = ""
    description: str = ""
    universe: list[str] | Screener = []
    timeframe: Any = "1H"
    can_long: bool = True
    can_short: bool = True
    warmup: int = 0
    schedule: dict[str, str] = {}

    def __init__(self, params: dict | None = None):
        self._params: dict[str, float] = {}
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
    def default_timeframe(cls) -> str:
        tf = vars_lookup(cls, "timeframe")
        return tf.default if isinstance(tf, Param) else str(tf)

    @classmethod
    def batched(cls) -> bool:
        """Whether the class handles a whole bar time at once (overrides on_bars)."""
        return cls.on_bars is not Strategy.on_bars

    @classmethod
    def meta(cls) -> dict:
        u = cls.universe
        universe = {"screener": u.to_wire()} if isinstance(u, Screener) else {"static": list(u or [])}
        doc = (cls.__doc__ or "").strip()
        meta: dict[str, Any] = {"canLong": bool(cls.can_long), "canShort": bool(cls.can_short)}
        if cls.warmup:
            meta["warmup"] = int(cls.warmup)
        if cls.schedule:
            meta["schedule"] = dict(cls.schedule)
        if cls.batched():
            meta["batch"] = True
        return {"class": cls.__name__, "name": cls.strategy_name(), "description": cls.description or doc,
                "timeframe": cls.default_timeframe(), "params": [p.spec() for p in cls.param_specs().values()],
                "universe": universe, "meta": meta}

    # ---- helpers ----
    def allows(self, side: str) -> bool:
        """Whether this instance may open `side` ("long" / "short"): the class's can_long / can_short and,
        when the strategy declares one, its `direction` parameter (long / short / both)."""
        if side not in ("long", "short"):
            raise ValueError(f"side must be long or short, not {side!r}")
        if not (self.can_long if side == "long" else self.can_short):
            return False
        d = getattr(self, "direction", "both")
        return d in ("both", side) if isinstance(d, str) else True

    # ---- handlers ----
    def on_start(self, ctx):
        """Once after the process starts or restarts (ctx.state is restored)."""

    def on_bar(self, ctx, bar):
        """One confirmed bar of one universe instrument (bar.inst bar.ts bar.o bar.h bar.l bar.c bar.vol bar.volq)."""

    def on_bars(self, ctx, bars):
        """All universe bars that closed at one time, {inst: Bar}. Override it to rank instruments
        against each other; the default calls on_bar for each one."""
        out = []
        for inst in sorted(bars):
            r = self.on_bar(ctx, bars[inst])
            if r is not None:
                out.extend(r if isinstance(r, (list, tuple)) else [r])
        return out

    def on_timer(self, ctx, name):
        """A timer from `schedule` is due."""

    def on_universe_change(self, ctx, added, removed):
        """A Screener universe changed; ctx.universe is current."""

    def on_fill(self, ctx, fill):
        """A fill of this instance (fill.inst_id fill.pos_side fill.side fill.qty fill.px fill.kind …)."""

    def on_position_closed(self, ctx, trade):
        """A position of this instance closed: trade.inst_id trade.pos_side trade.exit_px trade.kind
        (fill = by a signal, stop, target, time, liquidation) and trade.pnl (the closing fill's
        realized PnL minus its fee; earlier partial closes, entry fees and funding are not included)."""

    def on_signal_result(self, ctx, result):
        """result.signal_id result.outcome (ordered rejected skipped_ai …) result.reason."""

    def on_funding(self, ctx, inst, rate, amount):
        """Funding settled on a position."""

    def on_stop(self, ctx):
        """Before the process exits."""

    def status(self, ctx) -> str | None:
        """One short line shown on the instance card ("BTC: fast above slow, waiting"), or None."""
        return None


def vars_lookup(cls, name: str) -> Any:
    for klass in cls.__mro__:
        if name in vars(klass):
            return vars(klass)[name]
    return None
