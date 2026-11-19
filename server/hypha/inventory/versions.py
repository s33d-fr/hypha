"""Comparaison de versions de paquets à la façon de dpkg (`dpkg --compare-versions`).

Forme : `[époque:]amont[-révision]`. L'époque se compare en nombre ; l'amont puis la révision
alternent des parties non numériques (lettres avant les autres signes, `~` avant tout, même la fin
de chaîne) et des parties numériques comparées en valeur. Sert aussi, faute de mieux, aux
versions rpm et Windows (`3.5.2.0`), qui suivent le même ordre dans les cas courants.
"""
import re

_EPOCH_RE = re.compile(r"^(\d+):(.*)$")


def _digit(c: str) -> bool:
    return "0" <= c <= "9"


def _order(c: str) -> int:
    if _digit(c):
        return 0
    if c.isascii() and c.isalpha():
        return ord(c)
    if c == "~":
        return -1
    return ord(c) + 256


def verrevcmp(a: str, b: str) -> int:
    """Algorithme `verrevcmp` de dpkg : négatif si a < b, 0 si égales, positif si a > b."""
    i = j = 0
    la, lb = len(a), len(b)
    while i < la or j < lb:
        first_diff = 0
        while (i < la and not _digit(a[i])) or (j < lb and not _digit(b[j])):
            ac = _order(a[i]) if i < la else 0
            bc = _order(b[j]) if j < lb else 0
            if ac != bc:
                return ac - bc
            i += 1
            j += 1
        while i < la and a[i] == "0":
            i += 1
        while j < lb and b[j] == "0":
            j += 1
        while i < la and j < lb and _digit(a[i]) and _digit(b[j]):
            if not first_diff:
                first_diff = ord(a[i]) - ord(b[j])
            i += 1
            j += 1
        if i < la and _digit(a[i]):
            return 1
        if j < lb and _digit(b[j]):
            return -1
        if first_diff:
            return first_diff
    return 0


def split_version(v: str) -> tuple[int, str, str]:
    v = (v or "").strip()
    epoch = 0
    m = _EPOCH_RE.match(v)
    if m:
        epoch, v = int(m.group(1)), m.group(2)
    upstream, sep, revision = v.rpartition("-")
    if not sep:
        upstream, revision = v, ""
    return epoch, upstream, revision


def compare(a: str, b: str) -> int:
    """-1, 0 ou 1 selon l'ordre dpkg de deux versions complètes."""
    ea, ua, ra = split_version(a)
    eb, ub, rb = split_version(b)
    if ea != eb:
        return -1 if ea < eb else 1
    c = verrevcmp(ua, ub) or verrevcmp(ra, rb)
    return (c > 0) - (c < 0)


def matches(version: str, lt: str | None = None, le: str | None = None, gt: str | None = None,
            ge: str | None = None, eq: str | None = None) -> bool:
    """Vrai si `version` respecte toutes les bornes données."""
    if lt is not None and compare(version, lt) >= 0:
        return False
    if le is not None and compare(version, le) > 0:
        return False
    if gt is not None and compare(version, gt) <= 0:
        return False
    if ge is not None and compare(version, ge) < 0:
        return False
    if eq is not None and compare(version, eq) != 0:
        return False
    return True
