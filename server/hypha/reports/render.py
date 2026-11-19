"""HTML sobre des rapports (contrat § 22), avec petites courbes SVG intégrées. Sans accès à la base.

Tout le texte passe par html.escape ; styles en ligne (les clients de courriel ignorent les feuilles
de style) ; les courbes sont des <svg> en ligne, sans script ni ressource externe.
"""
import html

ACCENT = "#2563eb"
MUTED = "#71717a"
STATE_COLORS = {"ok": "#16a34a", "warning": "#ca8a04", "breached": "#dc2626", "ALERT": "#dc2626",
                "WARN": "#ca8a04", "OK": "#16a34a"}


def esc(v) -> str:
    return html.escape("" if v is None else str(v))


def fmt(v, digits: int = 4) -> str:
    if v is None:
        return "—"
    if isinstance(v, float):
        if abs(v) >= 1e6 or (abs(v) < 1e-3 and v != 0):
            return f"{v:.3g}"
        return f"{v:.{digits}g}"
    return str(v)


def sparkline(values: list, width: int = 120, height: int = 28, color: str = ACCENT) -> str:
    """Petite courbe : une polyligne par segment de points présents (un trou coupe la ligne)."""
    pts = [(i, v) for i, v in enumerate(values) if v is not None]
    if len(pts) < 2:
        return f'<svg width="{width}" height="{height}" xmlns="http://www.w3.org/2000/svg"></svg>'
    lo = min(v for _, v in pts)
    hi = max(v for _, v in pts)
    span = (hi - lo) or 1.0
    n = max(1, len(values) - 1)
    segments, cur = [], []
    for i, v in enumerate(values):
        if v is None:
            if len(cur) > 1:
                segments.append(cur)
            cur = []
            continue
        x = 1 + (width - 2) * i / n
        y = height - 2 - (height - 4) * (v - lo) / span
        cur.append(f"{x:.1f},{y:.1f}")
    if len(cur) > 1:
        segments.append(cur)
    lines = "".join(f'<polyline fill="none" stroke="{color}" stroke-width="1.5" points="{" ".join(s)}"/>'
                    for s in segments)
    return f'<svg width="{width}" height="{height}" viewBox="0 0 {width} {height}" xmlns="http://www.w3.org/2000/svg">{lines}</svg>'


def table(headers: list[str], rows: list[list], raw_cols: tuple = ()) -> str:
    """Tableau ; les colonnes de `raw_cols` portent déjà du HTML sûr (courbes, pastilles)."""
    th = "".join(f'<th style="text-align:left;padding:4px 10px 4px 0;color:{MUTED};font-weight:600;'
                 f'border-bottom:1px solid #e4e4e7">{esc(h)}</th>' for h in headers)
    body = []
    for r in rows:
        cells = "".join(f'<td style="padding:4px 10px 4px 0;border-bottom:1px solid #f4f4f5;vertical-align:middle">'
                        f'{c if i in raw_cols else esc(c)}</td>' for i, c in enumerate(r))
        body.append(f"<tr>{cells}</tr>")
    return (f'<table style="border-collapse:collapse;font-size:13px;width:100%">'
            f'<thead><tr>{th}</tr></thead><tbody>{"".join(body)}</tbody></table>')


def badge(text: str, key: str | None = None) -> str:
    color = STATE_COLORS.get(key or text, MUTED)
    return f'<span style="color:{color};font-weight:600">{esc(text)}</span>'


def section(title: str, body: str) -> str:
    return (f'<h2 style="font-size:16px;margin:24px 0 8px;border-left:4px solid {ACCENT};padding-left:8px">'
            f'{esc(title)}</h2>{body}')


def paragraph(text: str) -> str:
    return f'<p style="margin:6px 0;font-size:14px">{esc(text)}</p>'


def page(title: str, subtitle: str, sections: list[str], url: str) -> str:
    return (f'<!doctype html><html><head><meta charset="utf-8"><title>{esc(title)}</title></head>'
            f'<body style="margin:0;padding:16px;background:#ffffff">'
            f'<div style="font-family:system-ui,-apple-system,Segoe UI,Arial,sans-serif;max-width:760px;color:#18181b">'
            f'<h1 style="font-size:20px;margin:0 0 4px">{esc(title)}</h1>'
            f'<p style="margin:0 0 8px;color:{MUTED};font-size:13px">{esc(subtitle)}</p>'
            f'{"".join(sections)}'
            f'<p style="margin:28px 0 0;color:{MUTED};font-size:12px">HYPHA, supervision S33D · '
            f'<a href="{esc(url)}" style="color:{ACCENT}">ouvrir HYPHA</a></p>'
            f'</div></body></html>')


def duration(seconds: float) -> str:
    s = int(seconds)
    if s < 60:
        return f"{s} s"
    if s < 3600:
        return f"{s // 60} min"
    if s < 86400:
        return f"{s // 3600} h {s % 3600 // 60:02d}"
    return f"{s // 86400} j {s % 86400 // 3600} h"
