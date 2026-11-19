"""Appel du modèle (API de type Ollama, `POST {url}/api/chat`), sans diffusion ni « réflexion »."""
import re
import time
from collections import deque

import httpx

_THINK_RE = re.compile(r"<think>.*?</think>", re.S | re.I)


class LlmUnavailable(Exception):
    pass


def strip_think(text: str) -> str:
    """Retire un bloc <think>…</think> ; certains modèles omettent la balise ouvrante."""
    text = _THINK_RE.sub("", text or "")
    if "</think>" in text.lower():
        text = text[text.lower().rfind("</think>") + len("</think>"):]
    return text.strip()


async def chat(url: str, model: str, messages: list[dict], timeout_s: float,
               transport: httpx.AsyncBaseTransport | None = None) -> tuple[str, int]:
    """Rend (texte, durée en ms). Lève LlmUnavailable si le modèle ne répond pas utilement.

    `transport` ne sert qu'aux tests (httpx.MockTransport) : en service, la vraie pile réseau.
    """
    payload = {"model": model, "messages": messages, "stream": False, "think": False,
               "options": {"temperature": 0.2}}
    t0 = time.monotonic()
    try:
        async with httpx.AsyncClient(timeout=timeout_s, transport=transport) as client:
            r = await client.post(url + "/api/chat", json=payload)
    except httpx.TimeoutException:
        raise LlmUnavailable(f"délai de {timeout_s:g} s dépassé")
    except httpx.HTTPError as e:
        raise LlmUnavailable(f"modèle injoignable : {e}")
    duration_ms = int((time.monotonic() - t0) * 1000)
    if r.status_code != 200:
        raise LlmUnavailable(f"réponse {r.status_code} du modèle")
    try:
        content = (r.json().get("message") or {}).get("content")
    except ValueError:
        content = None
    text = strip_think(content or "")
    if not text:
        raise LlmUnavailable("réponse vide du modèle")
    return text, duration_ms


class HourlyQuota:
    """Au plus `limit` explications par heure glissante et par organisation (mémoire du processus)."""

    def __init__(self, limit: int = 60, window_s: float = 3600.0):
        self.limit = limit
        self.window_s = window_s
        self._calls: dict[int, deque] = {}

    def allow(self, org_id: int, now: float | None = None) -> bool:
        now = time.monotonic() if now is None else now
        calls = self._calls.setdefault(org_id, deque())
        while calls and now - calls[0] >= self.window_s:
            calls.popleft()
        if len(calls) >= self.limit:
            return False
        calls.append(now)
        return True
