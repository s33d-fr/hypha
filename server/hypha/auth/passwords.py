"""Mots de passe des comptes locaux (auto-hébergé) : scrypt de la bibliothèque standard.

Format stocké : `scrypt$<n>$<r>$<p>$<sel base64>$<empreinte base64>`, ce qui permet de durcir
les paramètres plus tard sans invalider les comptes existants.
"""
import base64
import hashlib
import hmac
import os

N, R, P = 2 ** 15, 8, 1
DKLEN = 32
MAXMEM = 64 * 1024 * 1024          # n=2^15, r=8 demandent 32 Mio : au-delà de la limite par défaut


def _b64(b: bytes) -> str:
    return base64.b64encode(b).decode("ascii")


def hash_password(password: str) -> str:
    salt = os.urandom(16)
    dk = hashlib.scrypt(password.encode("utf-8"), salt=salt, n=N, r=R, p=P, maxmem=MAXMEM, dklen=DKLEN)
    return f"scrypt${N}${R}${P}${_b64(salt)}${_b64(dk)}"


def verify_password(password: str, stored: str | None) -> bool:
    if not stored:
        # Calcul factice : un identifiant inconnu coûte autant qu'un mot de passe faux.
        hashlib.scrypt(password.encode("utf-8"), salt=b"0" * 16, n=N, r=R, p=P, maxmem=MAXMEM, dklen=DKLEN)
        return False
    try:
        algo, n, r, p, salt_b64, dk_b64 = stored.split("$")
        if algo != "scrypt":
            return False
        salt, expected = base64.b64decode(salt_b64), base64.b64decode(dk_b64)
        dk = hashlib.scrypt(password.encode("utf-8"), salt=salt, n=int(n), r=int(r), p=int(p),
                            maxmem=MAXMEM * 4, dklen=len(expected))
    except (ValueError, TypeError):
        return False
    return hmac.compare_digest(dk, expected)
