"""Test synthétique multi-étapes (contrat § 29) : requêtes HTTP enchaînées, variables, assertions.

Les étapes partagent un client (cookies de session compris). Chaque URL, chaque redirection
suivie, passe par la garde réseau (adresse non publique refusée sur le SaaS). Une étape en échec
arrête le test ; `details.steps` porte la durée, le statut et les assertions de chaque étape jouée.
"""
import socket
import time

import httpx

from . import assertions as A
from .netguard import TargetRefused, resolve
from .probes import MAX_BODY, REDIRECT_CODES, USER_AGENT, ProbeResult

MAX_STEPS = 20


async def _exchange(client: httpx.AsyncClient, method: str, url: str, headers: dict, body, follow: bool,
                    allow_private: bool) -> tuple[httpx.Response, str, int]:
    """Une requête, redirections suivies au besoin ; rend (réponse, corps texte, nombre de redirections)."""
    redirects = 0
    while True:
        parsed = httpx.URL(url)
        if parsed.scheme not in ("http", "https") or not parsed.host:
            raise TargetRefused(f"URL refusée : {url}")
        await resolve(parsed.host, parsed.port or (443 if parsed.scheme == "https" else 80), allow_private)
        req = client.build_request(method, url, headers=headers, content=body)
        resp = await client.send(req, stream=True)
        try:
            chunks, size = [], 0
            async for chunk in resp.aiter_bytes():
                size += len(chunk)
                if size <= MAX_BODY:
                    chunks.append(chunk)
        finally:
            await resp.aclose()
        location = resp.headers.get("location")
        if follow and resp.status_code in REDIRECT_CODES and location and redirects < 10:
            url = str(resp.url.join(location))
            redirects += 1
            if resp.status_code == 303 or (resp.status_code in (301, 302) and method == "POST"):
                method, body = "GET", None
            continue
        text = b"".join(chunks).decode(resp.encoding or "utf-8", errors="replace")
        return resp, text, redirects


async def run_multistep(steps: list[dict], cfg: dict, allow_private: bool,
                        transport: httpx.AsyncBaseTransport | None = None) -> ProbeResult:
    """`transport` ne sert qu'aux tests (httpx.MockTransport) : en service, la vraie pile réseau."""
    variables = {str(k): str(v) for k, v in (cfg.get("variables") or {}).items()}
    played: list[dict] = []
    start = time.perf_counter()
    status_code = None
    error = None
    async with httpx.AsyncClient(follow_redirects=False, verify=True, transport=transport) as client:
        for i, step in enumerate(steps[:MAX_STEPS]):
            name = step.get("name") or f"étape {i + 1}"
            missing: list[str] = []
            url = A.render(step["url"], variables, missing)
            headers = {"User-Agent": USER_AGENT,
                       **{k: A.render(v, variables, missing) for k, v in (step.get("headers") or {}).items()}}
            body = A.render(step.get("body") or "", variables, missing) or None
            record = {"name": name, "method": step.get("method", "GET"), "url": url}
            if missing:
                error = f"{name} : variable inconnue {', '.join(sorted(set(missing)))}"
                record.update({"ok": False, "error": error})
                played.append(record)
                break
            timeout = float(step.get("timeout_s") or cfg.get("timeout_s") or 10)
            client.timeout = httpx.Timeout(timeout)
            t0 = time.perf_counter()
            try:
                resp, text, redirects = await _exchange(client, record["method"], url, headers, body,
                                                        bool(step.get("follow_redirects", True)), allow_private)
            except TargetRefused as e:
                error = f"{name} : {e}"
            except socket.gaierror as e:
                error = f"{name} : résolution DNS impossible : {e}"
            except httpx.TimeoutException:
                error = f"{name} : délai dépassé ({timeout:g} s)"
            except httpx.HTTPError as e:
                error = f"{name} : connexion impossible : {e}"
            except (OSError, ValueError) as e:
                error = f"{name} : erreur réseau : {e}"
            duration = round((time.perf_counter() - t0) * 1000, 2)
            record["duration_ms"] = duration
            if error:
                record.update({"ok": False, "error": error})
                played.append(record)
                break
            status_code = resp.status_code
            response = A.Response(resp.status_code, {k.lower(): v for k, v in resp.headers.items()}, text, duration)
            checks = step.get("assertions") or [{"type": "status", "op": "lt", "value": 400}]
            results = A.evaluate(checks, response)
            found, not_found = A.extract(step.get("extract") or [], response)
            variables.update(found)
            failed = [r for r in results if not r["ok"]]
            record.update({"status_code": resp.status_code, "redirects": redirects, "assertions": results,
                           "extracted": sorted(found), "ok": not failed and not not_found})
            if not_found:
                record["error"] = f"extraction sans résultat : {', '.join(not_found)}"
            elif failed:
                record["error"] = "; ".join(A.describe_failure(r) for r in failed)
            played.append(record)
            if not record["ok"]:
                error = f"{name} : {record['error']}"
                break
    latency = round((time.perf_counter() - start) * 1000, 2)
    ok = error is None and len(played) == min(len(steps), MAX_STEPS) and bool(steps)
    if not steps:
        error = "aucune étape"
    return ProbeResult(ok, status_code, latency, error, {"steps": played})
