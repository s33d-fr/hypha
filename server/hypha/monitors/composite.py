"""Expression d'un moniteur composite (contrat § 19) : `12 && (13 || !14)`, sans `eval`.

Grammaire : ou := et ('||' et)* ; et := unaire ('&&' unaire)* ; unaire := '!' unaire | '(' ou ')' | id.
L'arbre est fait de tuples : ("id", 12), ("not", n), ("and", [n…]), ("or", [n…]).
"""
import re

MAX_LEN = 1000
MAX_IDS = 50
MAX_DEPTH = 20
_TOKEN_RE = re.compile(r"\s*(?:(\d{1,18})|(&&)|(\|\|)|(!)|(\()|(\)))")


def tokenize(expr: str) -> list[tuple[str, str]]:
    if not isinstance(expr, str) or not expr.strip():
        raise ValueError("expression vide")
    if len(expr) > MAX_LEN:
        raise ValueError(f"expression trop longue ({MAX_LEN} caractères au plus)")
    out, i = [], 0
    text = expr.rstrip()
    while i < len(text):
        m = _TOKEN_RE.match(text, i)
        if not m or m.end() == i:
            raise ValueError(f"caractère inattendu à la position {i + 1} : « {text[i:i + 10].strip()} »")
        kind = ("id", "and", "or", "not", "lpar", "rpar")[m.lastindex - 1]
        out.append((kind, m.group(m.lastindex)))
        i = m.end()
    return out


class _Parser:
    def __init__(self, toks):
        self.toks = toks
        self.i = 0

    def peek(self):
        return self.toks[self.i][0] if self.i < len(self.toks) else None

    def take(self):
        tok = self.toks[self.i]
        self.i += 1
        return tok

    def parse_or(self, depth):
        nodes = [self.parse_and(depth)]
        while self.peek() == "or":
            self.take()
            nodes.append(self.parse_and(depth))
        return nodes[0] if len(nodes) == 1 else ("or", nodes)

    def parse_and(self, depth):
        nodes = [self.parse_unary(depth)]
        while self.peek() == "and":
            self.take()
            nodes.append(self.parse_unary(depth))
        return nodes[0] if len(nodes) == 1 else ("and", nodes)

    def parse_unary(self, depth):
        if depth > MAX_DEPTH:
            raise ValueError("expression trop imbriquée")
        kind = self.peek()
        if kind is None:
            raise ValueError("expression incomplète")
        if kind == "not":
            self.take()
            return ("not", self.parse_unary(depth + 1))
        if kind == "lpar":
            self.take()
            node = self.parse_or(depth + 1)
            if self.peek() != "rpar":
                raise ValueError("parenthèse non fermée")
            self.take()
            return node
        if kind == "id":
            return ("id", int(self.take()[1]))
        raise ValueError("opérateur mal placé : un identifiant de moniteur est attendu")


def parse(expr: str):
    parser = _Parser(tokenize(expr))
    node = parser.parse_or(0)
    if parser.i != len(parser.toks):
        raise ValueError("parenthèse fermante ou opérateur en trop")
    if len(ids(node)) > MAX_IDS:
        raise ValueError(f"{MAX_IDS} moniteurs au plus dans une expression")
    return node


def ids(node) -> set[int]:
    kind = node[0]
    if kind == "id":
        return {node[1]}
    if kind == "not":
        return ids(node[1])
    out: set[int] = set()
    for n in node[1]:
        out |= ids(n)
    return out


def evaluate(node, truth: dict[int, bool]) -> bool:
    """Valeur de l'expression ; un moniteur absent de `truth` vaut faux."""
    kind = node[0]
    if kind == "id":
        return bool(truth.get(node[1], False))
    if kind == "not":
        return not evaluate(node[1], truth)
    if kind == "and":
        return all(evaluate(n, truth) for n in node[1])
    return any(evaluate(n, truth) for n in node[1])


def find_cycle(graph: dict[int, set[int]], start: int) -> list[int] | None:
    """Cycle atteignable depuis `start` dans le graphe des composites (id → ids cités), ou None."""
    path: list[int] = []
    on_path: set[int] = set()
    done: set[int] = set()

    def visit(n: int) -> list[int] | None:
        if n in on_path:
            return path[path.index(n):] + [n]
        if n in done or n not in graph:
            return None
        on_path.add(n)
        path.append(n)
        for nxt in sorted(graph[n]):
            found = visit(nxt)
            if found:
                return found
        path.pop()
        on_path.discard(n)
        done.add(n)
        return None

    return visit(start)
