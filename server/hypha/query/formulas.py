"""Formules et fonctions de `POST /metrics/query` (contrat § 30) : analyse sûre, calcul sur séries alignées.

Requête : `q` est une requête de métriques, éventuellement enveloppée de fonctions :

    timeshift(avg:system.cpu.usage{host:web-01}, -604800)
    top(per_second(sum:logs.http.errors{*} by {service}.rollup(sum, 60)), 5, 'max', 'desc')

Formule : identifiants de requêtes, constantes, `+ - * /`, parenthèses et les mêmes fonctions :
`a / b * 100`, `moving_avg(a / b, 5)`.

Aucun `eval` : un analyseur descendant construit un arbre fermé (nombre, chaîne, référence,
requête, appel d'une fonction connue, opération), borné en taille et en profondeur. Les séries de
deux opérandes s'alignent par groupe (étiquettes communes égales ; une série sans groupe s'applique
à toutes) et par instant (même seau de la grille).
"""
import math
import re
from dataclasses import dataclass, field

from ..errors import QueryError
from . import metrics_ql as mql
from . import series_fn as F

MAX_NODES = 100
MAX_DEPTH = 12
MAX_TEXT = 2000

# Arguments après la série : int, num (nombre), str (chaîne) ; « ? » = facultatif.
FUNCTIONS: dict[str, tuple] = {
    "abs": (), "log2": (), "log10": (), "cumsum": (), "per_second": (), "rate": (), "derivative": (),
    "moving_avg": ("int",), "ewma": ("int",), "median": ("int",), "timeshift": ("int",),
    "top": ("int", "str?", "str?"), "clamp_min": ("num",), "clamp_max": ("num",), "fill": ("str",),
    "forecast": ("str", "int"), "anomalies": ("str", "num?"), "outliers": ("str", "num?"),
}
CHOICES = {
    ("top", 1): ("mean", "avg", "max", "min", "last", "sum", "area"), ("top", 2): ("desc", "asc"),
    ("fill", 0): ("null", "zero", "last", "linear"), ("forecast", 0): ("linear", "seasonal"),
    ("anomalies", 0): ("basic", "agile"), ("outliers", 0): ("mad",),
}
DEFAULTS = {"top": [None, "mean", "desc"], "anomalies": [None, 2.0], "outliers": [None, 3.0]}
BOUNDS = {("moving_avg", 0): (1, 1000), ("ewma", 0): (1, 1000), ("median", 0): (1, 1000),
          ("timeshift", 0): (-400 * 86400, 400 * 86400), ("top", 0): (1, 100), ("forecast", 1): (60, 90 * 86400),
          ("anomalies", 1): (0.1, 10.0), ("outliers", 1): (0.1, 20.0)}


# ── arbre ──────────────────────────────────────────────────────────────────────

@dataclass
class Num:
    value: float


@dataclass
class Str:
    value: str


@dataclass
class Ref:
    name: str


@dataclass
class Metric:
    text: str
    mq: mql.MetricQuery


@dataclass
class Call:
    name: str
    args: list


@dataclass
class BinOp:
    op: str
    left: object
    right: object


@dataclass
class Neg:
    node: object


def _walk(node):
    yield node
    if isinstance(node, Call):
        for a in node.args:
            yield from _walk(a)
    elif isinstance(node, BinOp):
        yield from _walk(node.left)
        yield from _walk(node.right)
    elif isinstance(node, Neg):
        yield from _walk(node.node)


def refs(node) -> set[str]:
    return {n.name for n in _walk(node) if isinstance(n, Ref)}


def is_plain(node) -> bool:
    """Requête sans fonction : servie par le chemin d'origine (format 0.3.0 inchangé)."""
    return isinstance(node, Metric)


def leaf_metric(node) -> mql.MetricQuery | None:
    return next((n.mq for n in _walk(node) if isinstance(n, Metric)), None)


def _check_size(node) -> None:
    count = 0

    def depth(n, d):
        nonlocal count
        count += 1
        if count > MAX_NODES:
            raise QueryError(f"expression trop grande ({MAX_NODES} éléments au plus)")
        if d > MAX_DEPTH:
            raise QueryError("expression trop imbriquée")
        if isinstance(n, Call):
            for a in n.args:
                depth(a, d + 1)
        elif isinstance(n, BinOp):
            depth(n.left, d + 1)
            depth(n.right, d + 1)
        elif isinstance(n, Neg):
            depth(n.node, d + 1)
    depth(node, 0)


def _unquote(s: str) -> str:
    body = s[1:-1]
    return re.sub(r"\\(.)", r"\1", body)


def _number_of(node) -> float | None:
    if isinstance(node, Num):
        return node.value
    if isinstance(node, Neg) and isinstance(node.node, Num):
        return -node.node.value
    return None


def make_call(name: str, args: list) -> Call:
    spec = FUNCTIONS.get(name)
    if spec is None:
        raise QueryError(f"fonction inconnue : {name}")
    if not args:
        raise QueryError(f"{name}() attend une série en premier argument")
    if isinstance(args[0], (Num, Str)):
        raise QueryError(f"{name}() : le premier argument est une série")
    extra = list(args[1:])
    required = sum(1 for k in spec if not k.endswith("?"))
    if not required <= len(extra) <= len(spec):
        raise QueryError(f"{name}() attend {required} à {len(spec)} argument(s) après la série")
    defaults = DEFAULTS.get(name)
    if defaults:
        extra += [Str(d) if isinstance(d, str) else Num(d) for d in defaults[len(extra):]]
    out = []
    for i, (kind, a) in enumerate(zip(spec, extra)):
        kind = kind.rstrip("?")
        if kind == "str":
            if not isinstance(a, Str):
                raise QueryError(f"{name}() : argument {i + 2} entre guillemets attendu")
            choices = CHOICES.get((name, i))
            if choices and a.value not in choices:
                raise QueryError(f"{name}() : argument {i + 2} attendu parmi {', '.join(choices)}")
            out.append(a)
            continue
        v = _number_of(a)
        if v is None:
            raise QueryError(f"{name}() : argument {i + 2} numérique attendu")
        if kind == "int" and v != int(v):
            raise QueryError(f"{name}() : argument {i + 2} entier attendu")
        lo, hi = BOUNDS.get((name, i), (-1e18, 1e18))
        if not lo <= v <= hi:
            raise QueryError(f"{name}() : argument {i + 2} entre {lo:g} et {hi:g}")
        out.append(Num(int(v) if kind == "int" else float(v)))
    return Call(name, [args[0]] + out)


# ── formule ────────────────────────────────────────────────────────────────────

_TOKEN_RE = re.compile(r"\s*(?:(?P<num>(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?)|(?P<id>[A-Za-z_][A-Za-z0-9_]*)"
                       r"|(?P<str>'(?:[^'\\]|\\.)*'|\"(?:[^\"\\]|\\.)*\")|(?P<op>[-+*/(),]))")


def _tokens(text: str) -> list[tuple[str, str]]:
    out, pos = [], 0
    text = text.rstrip()
    while pos < len(text):
        m = _TOKEN_RE.match(text, pos)
        if not m or m.end() == pos:
            raise QueryError(f"caractère inattendu dans la formule : « {text[pos:pos + 10].strip()} »")
        kind = m.lastgroup
        out.append((kind, m.group(kind)))
        pos = m.end()
    return out


class _FormulaParser:
    def __init__(self, tokens):
        self.t = tokens
        self.i = 0

    def peek(self, value=None):
        if self.i >= len(self.t):
            return None
        tok = self.t[self.i]
        return tok if value is None or tok[1] == value else None

    def take(self, value=None):
        tok = self.peek(value)
        if tok is None:
            raise QueryError("formule incomplète" if value is None else f"« {value} » attendu dans la formule")
        self.i += 1
        return tok

    def expr(self):
        node = self.term()
        while self.peek("+") or self.peek("-"):
            op = self.take()[1]
            node = BinOp(op, node, self.term())
        return node

    def term(self):
        node = self.unary()
        while self.peek("*") or self.peek("/"):
            op = self.take()[1]
            node = BinOp(op, node, self.unary())
        return node

    def unary(self):
        if self.peek("-"):
            self.take()
            return Neg(self.unary())
        if self.peek("+"):
            self.take()
            return self.unary()
        return self.primary()

    def primary(self):
        kind, value = self.take()
        if kind == "num":
            v = float(value)
            if not math.isfinite(v):
                raise QueryError("nombre hors bornes")
            return Num(v)
        if kind == "str":
            return Str(_unquote(value))
        if kind == "id":
            if self.peek("("):
                self.take("(")
                args = []
                if not self.peek(")"):
                    args.append(self.expr())
                    while self.peek(","):
                        self.take(",")
                        args.append(self.expr())
                self.take(")")
                return make_call(value.lower(), args)
            return Ref(value)
        if value == "(":
            node = self.expr()
            self.take(")")
            return node
        raise QueryError(f"« {value} » inattendu dans la formule")


def parse_formula(text) -> object:
    if not isinstance(text, str) or not text.strip():
        raise QueryError("formule vide")
    if len(text) > MAX_TEXT:
        raise QueryError("formule trop longue")
    p = _FormulaParser(_tokens(text))
    node = p.expr()
    if p.i != len(p.t):
        raise QueryError(f"« {p.t[p.i][1]} » inattendu dans la formule")
    if isinstance(node, Str):
        raise QueryError("une formule ne peut pas être une chaîne")
    _check_size(node)
    return node


# ── requête enveloppée de fonctions ────────────────────────────────────────────

_CALL_HEAD = re.compile(r"^([a-z_][a-z0-9_]*)\s*\(")


def _split_top(text: str) -> list[str]:
    """Découpe aux virgules de premier niveau (hors (), {}, guillemets)."""
    parts, buf, depth, quote, escape = [], [], 0, None, False
    for ch in text:
        if quote:
            buf.append(ch)
            if escape:
                escape = False
            elif ch == "\\":
                escape = True
            elif ch == quote:
                quote = None
            continue
        if ch in "'\"":
            quote = ch
        elif ch in "({":
            depth += 1
        elif ch in ")}":
            depth -= 1
            if depth < 0:
                raise QueryError("parenthèse fermante en trop")
        elif ch == "," and depth == 0:
            parts.append("".join(buf))
            buf = []
            continue
        buf.append(ch)
    if quote or depth:
        raise QueryError("parenthèse ou guillemet non fermé")
    parts.append("".join(buf))
    return parts


def _closing(text: str, open_at: int) -> int:
    depth, quote, escape = 0, None, False
    for i in range(open_at, len(text)):
        ch = text[i]
        if quote:
            if escape:
                escape = False
            elif ch == "\\":
                escape = True
            elif ch == quote:
                quote = None
            continue
        if ch in "'\"":
            quote = ch
        elif ch in "({":
            depth += 1
        elif ch in ")}":
            depth -= 1
            if depth == 0:
                return i
    raise QueryError("parenthèse non fermée")


def _literal(text: str):
    t = text.strip()
    if len(t) >= 2 and t[0] == t[-1] and t[0] in "'\"":
        return Str(_unquote(t))
    try:
        v = float(t)
    except ValueError:
        raise QueryError(f"argument invalide : « {t[:50]} » (nombre ou chaîne entre guillemets)")
    if not math.isfinite(v):
        raise QueryError("nombre hors bornes")
    return Num(v)


def _parse_query(text: str, depth: int):
    if depth > MAX_DEPTH:
        raise QueryError("fonctions trop imbriquées")
    text = text.strip()
    m = _CALL_HEAD.match(text)
    if m and m.group(1) in FUNCTIONS:
        close = _closing(text, m.end() - 1)
        if close != len(text) - 1:
            raise QueryError(f"texte inattendu après {m.group(1)}(…)")
        args = _split_top(text[m.end():close])
        first = _parse_query(args[0], depth + 1)
        return make_call(m.group(1), [first] + [_literal(a) for a in args[1:]])
    return Metric(text, mql.parse(text))


def parse_query(text) -> object:
    if not isinstance(text, str) or not text.strip():
        raise QueryError("requête vide")
    if len(text) > MAX_TEXT:
        raise QueryError("requête trop longue")
    node = _parse_query(text, 0)
    _check_size(node)
    return node


def canonical(node) -> str:
    """Forme canonique d'un arbre (comparaison de requêtes d'un tableau partagé)."""
    if isinstance(node, Num):
        return repr(float(node.value))
    if isinstance(node, Str):
        return repr(node.value)
    if isinstance(node, Ref):
        return f"${node.name}"
    if isinstance(node, Metric):
        q = node.mq
        filters = sorted((f.key, f.value, f.negate, f.prefix, tuple(getattr(f, "values", None) or ()))
                         for f in q.filters)
        return repr((q.space_agg, q.metric, filters, tuple(q.group_by), q.rollup_fn, q.rollup_s))
    if isinstance(node, Call):
        return f"{node.name}(" + ",".join(canonical(a) for a in node.args) + ")"
    if isinstance(node, BinOp):
        return f"({canonical(node.left)}{node.op}{canonical(node.right)})"
    if isinstance(node, Neg):
        return f"-({canonical(node.node)})"
    raise QueryError("expression invalide")


# ── calcul sur séries alignées ─────────────────────────────────────────────────

@dataclass
class Series:
    group: dict
    values: list
    unit: str | None = None
    metric: str | None = None
    extra: dict = field(default_factory=dict)


def _gkey(group: dict) -> tuple:
    return tuple(sorted(group.items()))


def _pointwise(op: str, a, b):
    if a is None or b is None:
        return None
    if op == "+":
        r = a + b
    elif op == "-":
        r = a - b
    elif op == "*":
        r = a * b
    else:
        if b == 0:
            return None
        r = a / b
    return r if math.isfinite(r) else None


def _unit(op: str, ua, ub, a_is_num: bool, b_is_num: bool):
    if a_is_num:
        return ub
    if b_is_num:
        return ua
    if op in "+-":
        return ua if ua == ub else None
    return None


def binop(op: str, left, right):
    """Opération entre nombres et listes de séries ; rend un nombre ou une liste de séries."""
    if isinstance(left, float) and isinstance(right, float):
        r = _pointwise(op, left, right)
        return float("nan") if r is None else r
    if isinstance(left, float) or isinstance(right, float):
        num_left = isinstance(left, float)
        series = right if num_left else left
        k = left if num_left else right
        return [Series(s.group, [_pointwise(op, k, v) if num_left else _pointwise(op, v, k) for v in s.values],
                       _unit(op, None, s.unit, num_left, False) if num_left else _unit(op, s.unit, None, False, True),
                       s.metric) for s in series]
    out = []
    if len(left) == 1 and not left[0].group:
        pairs = [(left[0], r) for r in right]
    elif len(right) == 1 and not right[0].group:
        pairs = [(l, right[0]) for l in left]
    else:
        pairs = []
        for l in left:
            for r in right:
                common = set(l.group) & set(r.group)
                if all(l.group[k] == r.group[k] for k in common) and (common or not l.group or not r.group):
                    pairs.append((l, r))
    seen = set()
    for l, r in pairs:
        group = {**l.group, **r.group}
        key = _gkey(group)
        if key in seen:
            continue
        seen.add(key)
        n = min(len(l.values), len(r.values))
        out.append(Series(group, [_pointwise(op, l.values[i], r.values[i]) for i in range(n)],
                          _unit(op, l.unit, r.unit, False, False)))
    return out


def negate(value):
    if isinstance(value, float):
        return -value
    return [Series(s.group, [None if v is None else -v for v in s.values], s.unit, s.metric, dict(s.extra))
            for s in value]


def apply_simple(name: str, args: list, series: list[Series], step_s: int) -> list[Series]:
    """Fonctions qui ne relisent pas la base (toutes sauf timeshift, forecast et anomalies agile)."""
    a = [x.value if isinstance(x, (Num, Str)) else x for x in args]

    def each(fn):
        return [Series(s.group, fn(s.values), s.unit, s.metric, dict(s.extra)) for s in series]

    if name == "abs":
        return each(lambda v: F.map_values(v, abs))
    if name == "log2":
        return each(lambda v: F.log_base(v, 2))
    if name == "log10":
        return each(lambda v: F.log_base(v, 10))
    if name == "cumsum":
        return each(F.cumsum)
    if name == "per_second":
        return each(lambda v: F.per_second(v, step_s))
    if name == "rate":
        return each(lambda v: F.derivative(v, step_s, non_negative=True))
    if name == "derivative":
        return each(lambda v: F.derivative(v, step_s))
    if name == "moving_avg":
        return each(lambda v: F.moving_avg(v, a[0]))
    if name == "ewma":
        return each(lambda v: F.ewma(v, a[0]))
    if name == "median":
        return each(lambda v: F.rolling_median(v, a[0]))
    if name == "clamp_min":
        return each(lambda v: F.clamp(v, lo=a[0]))
    if name == "clamp_max":
        return each(lambda v: F.clamp(v, hi=a[0]))
    if name == "fill":
        return each(lambda v: F.fill(v, a[0]))
    if name == "top":
        keep = F.top_indices([s.values for s in series], a[0], a[1], a[2])
        return [series[i] for i in keep]
    if name == "outliers":
        scores = F.outlier_scores([s.values for s in series], a[1])
        out = []
        for s, score in zip(series, scores):
            extra = dict(s.extra)
            extra.update({"outlier": score > 0.2, "outlier_pct": round(score * 100, 1)})
            out.append(Series(s.group, s.values, s.unit, s.metric, extra))
        return out
    if name == "anomalies":           # basic : sur la série elle-même
        window = max(5, min(len(series[0].values) // 5 if series else 5, max(5, 7200 // max(1, step_s))))
        out = []
        for s in series:
            b = F.anomaly_bounds_basic(s.values, a[1], window)
            out.append(_with_bounds(s, b))
        return out
    raise QueryError(f"fonction {name} non calculable ici")


def _with_bounds(s: Series, bounds: list) -> Series:
    extra = dict(s.extra)
    extra["bounds_values"] = bounds
    extra["outside"] = sum(1 for v, b in zip(s.values, bounds) if v is not None and b is not None
                           and not b[0] <= v <= b[1])
    return Series(s.group, s.values, s.unit, s.metric, extra)
