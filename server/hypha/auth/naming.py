"""Noms et slugs d'organisation (sans accès à la base)."""
import re
import unicodedata

# Domaines de messagerie grand public : l'organisation prend alors le nom de la personne.
PUBLIC_MAIL_DOMAINS = {
    "gmail.com", "googlemail.com", "outlook.com", "outlook.fr", "hotmail.com", "hotmail.fr", "live.com",
    "live.fr", "msn.com", "yahoo.com", "yahoo.fr", "icloud.com", "me.com", "proton.me", "protonmail.com",
    "gmx.fr", "gmx.com", "gmx.de", "free.fr", "orange.fr", "wanadoo.fr", "sfr.fr", "laposte.net",
    "bbox.fr", "neuf.fr", "aol.com", "mail.com", "yandex.com", "zoho.com", "tutanota.com",
}


def slugify(text: str, default: str = "org") -> str:
    ascii_text = unicodedata.normalize("NFKD", text or "").encode("ascii", "ignore").decode()
    slug = re.sub(r"[^a-z0-9]+", "-", ascii_text.lower()).strip("-")[:40].strip("-")
    return slug or default


def org_name_from_email(email: str | None, fallback: str = "Mon organisation") -> str:
    if not email or "@" not in email:
        return fallback
    local, _, domain = email.lower().rpartition("@")
    if domain in PUBLIC_MAIL_DOMAINS:
        return local or fallback
    return domain.rsplit(".", 1)[0] or fallback
