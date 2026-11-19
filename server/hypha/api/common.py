"""Aides communes aux routes de l'interface."""
import json

from fastapi.responses import JSONResponse

from ..errors import bad_request
from ..util import dt_to_ms, json_default, now_ms

DEFAULT_RANGE_S = 3600
MAX_RANGE_DAYS = 800


class HyphaJSON(JSONResponse):
    """JSON direct : dates en ms Unix (contrat § 2), Decimal en nombre, sans passer par jsonable_encoder."""

    def render(self, content) -> bytes:
        return json.dumps(content, default=json_default, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def ok(data, status: int = 200) -> HyphaJSON:
    return HyphaJSON(data, status_code=status)


def done() -> HyphaJSON:
    return ok({"status": "ok"})


def time_range(from_: int | None, to: int | None, default_s: int = DEFAULT_RANGE_S) -> tuple[int, int]:
    now = now_ms()
    end = to if to is not None else now
    start = from_ if from_ is not None else end - default_s * 1000
    if start >= end:
        raise bad_request("`from` doit précéder `to`")
    if end - start > MAX_RANGE_DAYS * 86400 * 1000:
        raise bad_request(f"plage de plus de {MAX_RANGE_DAYS} jours")
    return start, end


def ms(dt) -> int | None:
    return dt_to_ms(dt)


def parse_tag_filter(tag: str) -> tuple[str, str]:
    key, sep, value = tag.partition(":")
    from ..util import TAG_KEY_RE
    if not sep or not TAG_KEY_RE.match(key) or not value:
        raise bad_request(f"filtre d'étiquette invalide : « {tag} » (attendu clé:valeur)")
    return key, value
