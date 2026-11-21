#!/usr/bin/env python3
"""Pages publiques de HYPHA : documentation, vidéos, FAQ, tarifs, pages légales, en français et en anglais.

    python3 site/build.py            # écrit les pages dans ui/ (docs/, en/, tarifs/…), sitemap.xml et robots.txt

Sources : site/pages/<langue>/*.md (en-tête « --- clé: valeur --- », puis Markdown réduit).
Bibliothèque standard seulement, aucun service extérieur : le résultat est versionné dans ui/ et
livré tel quel par Jenkins. Les vidéos ne sont pas dans le dépôt : elles sont servies sous /media/
(site/videos/publier-medias.sh).
"""
import html
import json
import os
import re
import sys
from datetime import date

ICI = os.path.dirname(os.path.abspath(__file__))
UI = os.path.join(os.path.dirname(ICI), "ui")
SITE = "https://hypha.s33d.fr"
LANGS = ("fr", "en")

TXT = {
    "fr": {
        "skip": "Aller au contenu", "docs": "Documentation", "pricing": "Tarifs", "faq": "FAQ", "login": "Se connecter",
        "by": "par S33D", "other": "English", "other_code": "EN", "menu": "Sommaire de la documentation",
        "home": "Accueil", "prev": "Précédent", "next": "Suivant", "updated": "Mise à jour : {d}",
        "foot_product": "HYPHA, un produit de S33D. Service hébergé en France, sur l'infrastructure en propre de S33D.",
        "legal": "Mentions légales", "terms": "Conditions de vente", "privacy": "Confidentialité", "copy": "Copier",
        "copied": "Copié", "transcript": "Ce que montre la vidéo", "video_fallback": "Votre navigateur ne lit pas cette vidéo.",
        "download": "Télécharger la vidéo (MP4)",
    },
    "en": {
        "skip": "Skip to content", "docs": "Documentation", "pricing": "Pricing", "faq": "FAQ", "login": "Sign in",
        "by": "by S33D", "other": "Français", "other_code": "FR", "menu": "Documentation contents",
        "home": "Home", "prev": "Previous", "next": "Next", "updated": "Updated: {d}",
        "foot_product": "HYPHA, a S33D product. Hosted in France, on S33D's own infrastructure.",
        "legal": "Legal notice", "terms": "Terms of sale", "privacy": "Privacy", "copy": "Copy",
        "copied": "Copied", "transcript": "What the video shows", "video_fallback": "Your browser cannot play this video.",
        "download": "Download the video (MP4)",
    },
}

# Sections de la documentation, dans l'ordre du sommaire ; chaque page déclare la sienne (section:).
SECTIONS = {
    "fr": ["Démarrer", "Installer l'agent", "Intégrations", "Observer", "Alerter et agir", "Administrer"],
    "en": ["Get started", "Install the agent", "Integrations", "Observe", "Alert and act", "Administer"],
}

# Vidéos servies sous /media/videos/ ; durée en secondes (ISO 8601 dans les données structurées).
VIDEOS = json.load(open(os.path.join(ICI, "videos", "videos.json"), encoding="utf-8"))


def esc(s):
    return html.escape(str(s), quote=True)


def slugify(s):
    s = s.lower()
    for a, b in (("à", "a"), ("â", "a"), ("ä", "a"), ("é", "e"), ("è", "e"), ("ê", "e"), ("ë", "e"), ("î", "i"), ("ï", "i"),
                 ("ô", "o"), ("ö", "o"), ("ù", "u"), ("û", "u"), ("ü", "u"), ("ç", "c"), ("œ", "oe"), ("’", "-"), ("'", "-")):
        s = s.replace(a, b)
    s = re.sub(r"<[^>]+>", "", s)
    s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
    return s or "section"


# ── Markdown réduit ────────────────────────────────────────────────────────────────────────────

def inline(s):
    """Code, liens, gras, italique ; le reste est échappé."""
    out, pos = [], 0
    for m in re.finditer(r"`([^`]+)`", s):
        out.append(_inline_text(s[pos:m.start()]))
        out.append("<code>" + esc(m.group(1)) + "</code>")
        pos = m.end()
    out.append(_inline_text(s[pos:]))
    return "".join(out)


def _inline_text(s):
    s = esc(s).replace("&#x27;", "'")
    s = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", lambda m: _link(m.group(1), m.group(2)), s)
    s = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", s)
    s = re.sub(r"(?<![\w*])\*([^*\s][^*]*)\*(?![\w*])", r"<em>\1</em>", s)
    return s


def _link(text, url):
    url = html.unescape(url)
    ext = url.startswith("http") and not url.startswith(SITE)
    attrs = ' rel="noopener"' if ext else ""
    return f'<a href="{esc(url)}"{attrs}>{text}</a>'


def markdown(src, ctx):
    lines = src.replace("\r\n", "\n").split("\n")
    out, i, toc = [], 0, []
    para = []

    def flush():
        if para:
            out.append("<p>" + inline(" ".join(x.strip() for x in para)) + "</p>")
            para.clear()

    while i < len(lines):
        line = lines[i]
        st = line.strip()
        if not st:
            flush(); i += 1; continue
        if st.startswith("```"):
            flush()
            lang = st[3:].strip()
            buf = []
            i += 1
            while i < len(lines) and not lines[i].strip().startswith("```"):
                buf.append(lines[i]); i += 1
            i += 1
            cls = f' class="language-{esc(lang)}"' if lang else ""
            out.append(f"<pre><code{cls}>" + esc("\n".join(buf)) + "</code></pre>")
            continue
        m = re.match(r"^(#{2,4})\s+(.*)$", st)
        if m:
            flush()
            level, text = len(m.group(1)), m.group(2)
            ident = slugify(text)
            n, base = 2, ident
            while ident in ctx["ids"]:
                ident = f"{base}-{n}"; n += 1
            ctx["ids"].add(ident)
            if level == 2:
                toc.append((ident, text))
            out.append(f'<h{level} id="{ident}">{inline(text)}<a class="anchor" href="#{ident}" aria-hidden="true">#</a></h{level}>')
            i += 1
            continue
        m = re.match(r"^\{\{video:([a-z0-9-]+)\}\}$", st)
        if m:
            flush(); out.append(video_block(m.group(1), ctx)); i += 1; continue
        if st.startswith("<"):
            flush()
            buf = []
            while i < len(lines) and lines[i].strip():
                buf.append(lines[i]); i += 1
            out.append("\n".join(buf))
            continue
        if st.startswith("|"):
            flush()
            rows = []
            while i < len(lines) and lines[i].strip().startswith("|"):
                cells = re.split(r"(?<!\\)\|", lines[i].strip()[1:-1] if lines[i].strip().endswith("|") else lines[i].strip()[1:])
                rows.append([c.strip().replace("\\|", "|") for c in cells]); i += 1
            head, body = rows[0], [r for r in rows[1:] if not all(re.match(r"^:?-{2,}:?$", c) for c in r)]
            t = ['<div class="table"><table><thead><tr>'] + [f"<th>{inline(c)}</th>" for c in head] + ["</tr></thead><tbody>"]
            for r in body:
                t.append("<tr>" + "".join(f"<td>{inline(c)}</td>" for c in r) + "</tr>")
            t.append("</tbody></table></div>")
            out.append("".join(t))
            continue
        if st.startswith(">"):
            flush()
            buf = []
            while i < len(lines) and lines[i].strip().startswith(">"):
                buf.append(lines[i].strip()[1:].strip()); i += 1
            out.append("<blockquote>" + markdown("\n".join(buf), {"ids": ctx["ids"], "lang": ctx["lang"]})[0] + "</blockquote>")
            continue
        if st == "---":
            flush(); out.append("<hr>"); i += 1; continue
        if re.match(r"^([-*]|\d+\.)\s+", st):
            flush()
            html_list, i = parse_list(lines, i)
            out.append(html_list)
            continue
        para.append(line)
        i += 1
    flush()
    return "\n".join(out), toc


def parse_list(lines, i):
    def indent(s):
        return len(s) - len(s.lstrip(" "))
    base = indent(lines[i])
    ordered = bool(re.match(r"^\s*\d+\.\s", lines[i]))
    items = []
    while i < len(lines):
        line = lines[i]
        if not line.strip():
            # Une ligne vide suivie d'un élément du même niveau garde la liste ouverte.
            if i + 1 < len(lines) and re.match(r"^\s*([-*]|\d+\.)\s+", lines[i + 1]) and indent(lines[i + 1]) >= base:
                i += 1; continue
            break
        ind = indent(line)
        m = re.match(r"^\s*([-*]|\d+\.)\s+(.*)$", line)
        if m and ind == base:
            items.append([m.group(2)]); i += 1; continue
        if m and ind > base:
            sub, i = parse_list(lines, i)
            items[-1].append(sub); continue
        if ind > base and items:
            items[-1].append(line.strip()); i += 1; continue
        break
    tag = "ol" if ordered else "ul"
    parts = []
    for it in items:
        text = [x for x in it if not x.startswith("<ul>") and not x.startswith("<ol>")]
        subs = [x for x in it if x.startswith("<ul>") or x.startswith("<ol>")]
        parts.append("<li>" + inline(" ".join(text)) + "".join(subs) + "</li>")
    return f"<{tag}>" + "".join(parts) + f"</{tag}>", i


def video_block(vid, ctx):
    v = VIDEOS[vid]
    lang = ctx["lang"]
    t = TXT[lang]
    base = f"/media/videos/{vid}-{lang}"
    steps = "".join(f"<li>{esc(s)}</li>" for s in v["steps"][lang])
    ctx.setdefault("videos", []).append(vid)
    return (f'<figure class="video" id="video-{vid}"><video controls preload="none" playsinline poster="{base}.jpg" '
            f'width="1280" height="720"><source src="{base}.mp4" type="video/mp4">'
            f'<source src="{base}.webm" type="video/webm">{esc(t["video_fallback"])} '
            f'<a href="{base}.mp4">{esc(t["download"])}</a></video>'
            f'<figcaption><strong>{esc(v["title"][lang])}</strong> · {v["duration"]} s</figcaption>'
            f'<details><summary>{esc(t["transcript"])}</summary><ol>{steps}</ol></details></figure>')


# ── Pages ──────────────────────────────────────────────────────────────────────────────────────

def read_pages():
    pages = []
    for lang in LANGS:
        folder = os.path.join(ICI, "pages", lang)
        for name in sorted(os.listdir(folder)):
            if not name.endswith(".md"):
                continue
            raw = open(os.path.join(folder, name), encoding="utf-8").read()
            m = re.match(r"^---\n(.*?)\n---\n(.*)$", raw, re.S)
            if not m:
                raise SystemExit(f"{lang}/{name} : en-tête absent")
            meta = {}
            for line in m.group(1).split("\n"):
                k, _, v = line.partition(":")
                v = v.strip()
                if len(v) >= 2 and v[0] == v[-1] == '"':
                    v = v[1:-1]  # valeur entre guillemets (titre qui porte « : »)
                meta[k.strip()] = v
            for k in ("id", "path", "title", "description"):
                if not meta.get(k):
                    raise SystemExit(f"{lang}/{name} : {k} manquant")
            meta["lang"], meta["body"], meta["file"] = lang, m.group(2), name
            meta["order"] = int(meta.get("order", "999"))
            pages.append(meta)
    return pages


def theme_script():
    return ("<script>try{var t=localStorage.getItem('hypha.theme');if(t==='light'||t==='dark')"
            "document.documentElement.setAttribute('data-theme',t)}catch(e){}</script>")


def page_html(p, pages, by_id):
    lang = p["lang"]
    t = TXT[lang]
    other = by_id.get((p["id"], "en" if lang == "fr" else "fr"))
    ctx = {"ids": set(), "lang": lang}
    body, toc = markdown(p["body"], ctx)
    kind = p.get("kind", "doc")
    docs = sorted([x for x in pages if x["lang"] == lang and x.get("kind", "doc") == "doc"], key=lambda x: x["order"])
    special = {x["id"]: x for x in pages if x["lang"] == lang}
    url = SITE + p["path"]

    head = [
        "<!doctype html>", f'<html lang="{lang}">', "<head>", '<meta charset="utf-8">',
        '<meta name="viewport" content="width=device-width, initial-scale=1">',
        f"<title>{esc(p['title'])}{'' if p['title'].startswith('HYPHA') else ' · HYPHA'}</title>",
        f'<meta name="description" content="{esc(p["description"])}">',
        f'<link rel="canonical" href="{url}">',
    ]
    if other:
        fr, en = (p, other) if lang == "fr" else (other, p)
        head += [f'<link rel="alternate" hreflang="fr" href="{SITE}{fr["path"]}">',
                 f'<link rel="alternate" hreflang="en" href="{SITE}{en["path"]}">',
                 f'<link rel="alternate" hreflang="x-default" href="{SITE}{fr["path"]}">']
    head += [
        '<meta name="color-scheme" content="dark light">',
        f'<meta property="og:title" content="{esc(p["title"])}">',
        f'<meta property="og:description" content="{esc(p["description"])}">',
        f'<meta property="og:url" content="{url}">', '<meta property="og:type" content="website">',
        f'<meta property="og:locale" content="{"fr_FR" if lang == "fr" else "en_GB"}">',
        '<meta property="og:site_name" content="HYPHA">',
        '<link rel="icon" type="image/svg+xml" href="/assets/img/hypha.svg">',
        '<link rel="stylesheet" href="/assets/css/site.css">', theme_script(),
    ]
    if ctx.get("videos"):
        head.append(f'<meta property="og:image" content="{SITE}/media/videos/{ctx["videos"][0]}-{lang}.jpg">')
    for block in structured(p, ctx, special, url):
        head.append('<script type="application/ld+json">' + json.dumps(block, ensure_ascii=False) + "</script>")
    head.append("</head>")

    def nav_link(key, label, extra=""):
        target = special.get(key)
        if not target:
            return ""
        cur = ' aria-current="page"' if (key == p["id"] or (key == "docs" and kind == "doc")) else ""
        return f'<a href="{target["path"]}"{cur}{extra}>{esc(label)}</a>'

    lang_href = other["path"] if other else ("/en/docs/" if lang == "fr" else "/docs/")
    hide_sm = ' class="hide-sm"'
    top = (f'<a class="skip" href="#contenu">{esc(t["skip"])}</a>'
           f'<header class="top"><div class="top-in"><a class="brand" href="/" aria-label="HYPHA">'
           f'<img src="/assets/img/hypha.svg" alt="" width="28" height="28"><span>HYPHA<small>{esc(t["by"])}</small></span></a>'
           f'<nav aria-label="HYPHA">{nav_link("docs", t["docs"])}{nav_link("pricing", t["pricing"])}'
           f'{nav_link("faq", t["faq"], hide_sm)}'
           f'<a class="lang" href="{lang_href}" hreflang="{"en" if lang == "fr" else "fr"}" data-lang="{"en" if lang == "fr" else "fr"}" '
           f'title="{esc(t["other"])}">{t["other_code"]}</a>'
           f'<a class="btn" href="/">{esc(t["login"])}</a></nav></div></header>')

    if kind == "doc":
        side = [f'<aside class="side"><details class="nav" open><summary>{esc(t["menu"])}</summary>']
        for sec in SECTIONS[lang]:
            items = [d for d in docs if d.get("section") == sec]
            if not items:
                continue
            side.append(f"<h2>{esc(sec)}</h2><ul>")
            for d in items:
                cur = ' aria-current="page"' if d is p else ""
                side.append(f'<li><a href="{d["path"]}"{cur}>{esc(d.get("nav", d["title"]))}</a></li>')
            side.append("</ul>")
        side.append("</details></aside>")
        idx = docs.index(p)
        prev_p, next_p = (docs[idx - 1] if idx > 0 else None), (docs[idx + 1] if idx + 1 < len(docs) else None)
        pager = '<nav class="pager" aria-label="pages">'
        pager += (f'<a href="{prev_p["path"]}"><small>{esc(t["prev"])}</small>{esc(prev_p.get("nav", prev_p["title"]))}</a>'
                  if prev_p else "<span></span>")
        pager += (f'<a href="{next_p["path"]}" style="text-align:right"><small>{esc(t["next"])}</small>'
                  f'{esc(next_p.get("nav", next_p["title"]))}</a>' if next_p else "<span></span>")
        pager += "</nav>"
        crumbs = "" if p["id"] == "docs" else (f'<div class="crumbs"><a href="{special["docs"]["path"]}">{esc(t["docs"])}</a> › '
                                                f'{esc(p.get("section", ""))}</div>')
        wrap_cls, side_html = "wrap", "".join(side)
    else:
        wrap_cls, side_html, pager, crumbs = "wrap single", "", "", ""

    updated = f'<p class="updated">{esc(t["updated"].format(d=fmt_date(p.get("updated"), lang)))}</p>' if p.get("updated") else ""
    lead = f'<p class="lead">{inline(p["lead"])}</p>' if p.get("lead") else ""
    main = (f'<div class="{wrap_cls}">{side_html}<main class="doc" id="contenu">{crumbs}<h1>{esc(p["title"])}</h1>{lead}'
            f"{body}{updated}{pager}</main></div>")
    foot = (f'<footer class="foot"><div class="foot-in"><span class="grow">{esc(t["foot_product"])}</span>'
            + "".join(nav_link(k, lab) for k, lab in (("docs", t["docs"]), ("pricing", t["pricing"]), ("faq", t["faq"]),
                                                       ("legal", t["legal"]), ("terms", t["terms"]), ("privacy", t["privacy"])))
            + '<a href="https://s33d.fr" rel="noopener">s33d.fr</a></div></footer>')
    script = ("<script>(function(){var c=" + json.dumps(t["copy"]) + ",d=" + json.dumps(t["copied"]) + ";"
              "document.querySelectorAll('.doc pre').forEach(function(p){var b=document.createElement('button');"
              "b.type='button';b.className='copy';b.textContent=c;b.onclick=function(){var x=p.querySelector('code');"
              "navigator.clipboard&&navigator.clipboard.writeText(x?x.innerText:p.innerText).then(function(){b.textContent=d;"
              "setTimeout(function(){b.textContent=c},1500)})};p.appendChild(b)});"
              "var n=document.querySelector('.side details');if(n&&window.matchMedia('(max-width: 860px)').matches)n.removeAttribute('open');"
              "document.querySelectorAll('a[data-lang]').forEach(function(a){a.addEventListener('click',function(){"
              "try{localStorage.setItem('hypha.lang',a.getAttribute('data-lang'))}catch(e){}})});"
              "})();</script>")
    return "\n".join(head) + f"\n<body>\n{top}\n{main}\n{foot}\n{script}\n</body>\n</html>\n"


def fmt_date(iso, lang):
    if not iso:
        return ""
    y, m, d = (int(x) for x in iso.split("-"))
    if lang == "fr":
        mois = ["janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"]
        return f"{d} {mois[m - 1]} {y}"
    return date(y, m, d).strftime("%d %B %Y").lstrip("0")


def structured(p, ctx, special, url):
    """Données structurées schema.org : fil d'Ariane, FAQ, vidéos, logiciel (tarifs)."""
    lang = p["lang"]
    out = []
    if p.get("kind", "doc") == "doc" and p["id"] != "docs" and "docs" in special:
        out.append({"@context": "https://schema.org", "@type": "BreadcrumbList", "itemListElement": [
            {"@type": "ListItem", "position": 1, "name": TXT[lang]["docs"], "item": SITE + special["docs"]["path"]},
            {"@type": "ListItem", "position": 2, "name": p["title"], "item": url}]})
    if p["id"] == "faq":
        qa = re.findall(r"^###\s+(.+?)\n(.*?)(?=^###\s|^##\s|\Z)", p["body"], re.M | re.S)
        out.append({"@context": "https://schema.org", "@type": "FAQPage", "mainEntity": [
            {"@type": "Question", "name": q.strip(),
             "acceptedAnswer": {"@type": "Answer", "text": re.sub(r"\s+", " ", re.sub(r"[`*]|\[([^\]]+)\]\([^)]+\)", r"\1", a)).strip()}}
            for q, a in qa]})
    for vid in ctx.get("videos", []):
        v = VIDEOS[vid]
        out.append({"@context": "https://schema.org", "@type": "VideoObject", "name": v["title"][lang],
                    "description": " ".join(v["steps"][lang]), "thumbnailUrl": f"{SITE}/media/videos/{vid}-{lang}.jpg",
                    "contentUrl": f"{SITE}/media/videos/{vid}-{lang}.mp4", "uploadDate": v["date"],
                    "duration": f"PT{v['duration']}S", "inLanguage": lang})
    if p["id"] == "pricing":
        out.append({"@context": "https://schema.org", "@type": "SoftwareApplication", "name": "HYPHA",
                    "applicationCategory": "BusinessApplication", "operatingSystem": "Linux, Windows",
                    "url": SITE, "publisher": {"@type": "Organization", "name": "S33D", "url": "https://s33d.fr"},
                    "offers": [
                        {"@type": "Offer", "name": "Libre", "price": "0", "priceCurrency": "EUR"},
                        {"@type": "Offer", "name": "Découverte", "price": "0", "priceCurrency": "EUR"},
                        {"@type": "Offer", "name": "Pro", "priceCurrency": "EUR", "priceSpecification": {
                            "@type": "UnitPriceSpecification", "price": "2.00", "priceCurrency": "EUR", "valueAddedTaxIncluded": False,
                            "referenceQuantity": {"@type": "QuantitativeValue", "value": 1, "unitText": "host / month"}}}]})
    return out


def write(path, content):
    full = os.path.join(UI, path.lstrip("/"))
    if full.endswith("/"):
        full += "index.html"
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "w", encoding="utf-8", newline="\n") as f:
        f.write(content)
    return full


def main():
    pages = read_pages()
    by_id = {(p["id"], p["lang"]): p for p in pages}
    paths = set()
    for p in pages:
        if p["path"] in paths:
            raise SystemExit(f"chemin en double : {p['path']}")
        paths.add(p["path"])
        if not p["path"].endswith("/"):
            raise SystemExit(f"{p['path']} : un chemin de page finit par /")
    for p in pages:
        write(p["path"], page_html(p, pages, by_id))
    # Plan du site : chaque page avec sa version dans l'autre langue. Les deux entrées de
    # l'accueil (ui/index.html et ui/en/index.html, écrites à la main) viennent en tête.
    accueil = "".join(f'<xhtml:link rel="alternate" hreflang="{lg}" href="{SITE}{u}"/>'
                      for lg, u in (("fr", "/"), ("en", "/en/"), ("x-default", "/")))
    sm = ['<?xml version="1.0" encoding="UTF-8"?>',
          '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">',
          f"<url><loc>{SITE}/</loc>{accueil}</url>", f"<url><loc>{SITE}/en/</loc>{accueil}</url>"]
    for p in sorted(pages, key=lambda x: x["path"]):
        sm.append(f"<url><loc>{SITE}{p['path']}</loc>")
        if p.get("updated"):
            sm.append(f"<lastmod>{p['updated']}</lastmod>")
        for lang in LANGS:
            q = by_id.get((p["id"], lang))
            if q:
                sm.append(f'<xhtml:link rel="alternate" hreflang="{lang}" href="{SITE}{q["path"]}"/>')
        sm.append("</url>")
    sm.append("</urlset>")
    write("/sitemap.xml", "\n".join(sm) + "\n")
    write("/robots.txt", "User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /v1/\nDisallow: /install/\nDisallow: /share/\n"
                         f"\nSitemap: {SITE}/sitemap.xml\n")
    print(f"{len(pages)} pages écrites dans {UI}")


if __name__ == "__main__":
    sys.exit(main())
