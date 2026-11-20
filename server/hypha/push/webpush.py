"""Web Push sans bibliothèque de plus : chiffrement aes128gcm (RFC 8291, RFC 8188) et en-tête VAPID
(RFC 8292), avec `cryptography` et PyJWT, déjà dépendances du serveur.

Clé VAPID : la clé privée P-256 en base64url (32 octets bruts, format des bibliothèques Web Push),
dans HYPHA_VAPID_PRIVATE_KEY ; la clé publique (65 octets, point non compressé) s'en déduit. Elle est
remise au navigateur à l'abonnement (`applicationServerKey`) et signe chaque envoi.
"""
import base64
import hashlib
import hmac
import os
import struct
import time
from urllib.parse import urlsplit

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

RECORD_SIZE = 4096            # rs de l'en-tête aes128gcm : un seul enregistrement
MAX_PLAINTEXT = 3800          # 4096 - en-tête (86) - étiquette (16) - délimiteur, avec de la marge
JWT_LIFETIME_S = 12 * 3600    # RFC 8292 : 24 h au plus


def b64u(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def unb64u(text: str) -> bytes:
    text = text.strip()
    return base64.urlsafe_b64decode(text + "=" * (-len(text) % 4))


def _hkdf(salt: bytes, ikm: bytes, info: bytes, length: int) -> bytes:
    """HKDF-SHA-256 (RFC 5869) en un seul bloc : length ≤ 32."""
    prk = hmac.new(salt, ikm, hashlib.sha256).digest()
    return hmac.new(prk, info + b"\x01", hashlib.sha256).digest()[:length]


def public_bytes(key: ec.EllipticCurvePublicKey) -> bytes:
    return key.public_bytes(serialization.Encoding.X962, serialization.PublicFormat.UncompressedPoint)


def load_public(raw: bytes) -> ec.EllipticCurvePublicKey:
    """Point P-256 non compressé (65 octets, 0x04 en tête) ; ValueError s'il n'est pas sur la courbe."""
    if len(raw) != 65 or raw[0] != 4:
        raise ValueError("clé p256dh : 65 octets attendus (point non compressé)")
    return ec.EllipticCurvePublicKey.from_encoded_point(ec.SECP256R1(), raw)


def load_private(b64: str) -> ec.EllipticCurvePrivateKey:
    raw = unb64u(b64)
    if len(raw) != 32:
        raise ValueError("clé privée VAPID : 32 octets en base64url attendus")
    return ec.derive_private_key(int.from_bytes(raw, "big"), ec.SECP256R1())


def private_b64(key: ec.EllipticCurvePrivateKey) -> str:
    return b64u(key.private_numbers().private_value.to_bytes(32, "big"))


def generate_vapid() -> tuple[str, str]:
    """Nouvelle paire VAPID : (privée, publique) en base64url."""
    key = ec.generate_private_key(ec.SECP256R1())
    return private_b64(key), b64u(public_bytes(key.public_key()))


def encrypt(plaintext: bytes, ua_public: bytes, auth_secret: bytes, *,
            as_private: ec.EllipticCurvePrivateKey | None = None, salt: bytes | None = None) -> bytes:
    """Corps chiffré d'un message push (Content-Encoding: aes128gcm).

    `as_private` et `salt` ne se passent que pour les tests (vecteurs de la RFC 8291) : en service, une
    clé éphémère et un sel neufs à chaque message."""
    if len(plaintext) > MAX_PLAINTEXT:
        raise ValueError(f"message push trop long : {len(plaintext)} octets ({MAX_PLAINTEXT} au plus)")
    if len(auth_secret) != 16:
        raise ValueError("secret auth : 16 octets attendus")
    ua_key = load_public(ua_public)
    as_private = as_private or ec.generate_private_key(ec.SECP256R1())
    as_public = public_bytes(as_private.public_key())
    salt = salt or os.urandom(16)
    ecdh_secret = as_private.exchange(ec.ECDH(), ua_key)
    ikm = _hkdf(auth_secret, ecdh_secret, b"WebPush: info\x00" + ua_public + as_public, 32)
    cek = _hkdf(salt, ikm, b"Content-Encoding: aes128gcm\x00", 16)
    nonce = _hkdf(salt, ikm, b"Content-Encoding: nonce\x00", 12)
    # Un seul enregistrement, le dernier : délimiteur 0x02, sans remplissage.
    ciphertext = AESGCM(cek).encrypt(nonce, plaintext + b"\x02", None)
    header = salt + struct.pack("!IB", RECORD_SIZE, len(as_public)) + as_public
    return header + ciphertext


def vapid_header(endpoint: str, private_key: ec.EllipticCurvePrivateKey, subject: str,
                 now: float | None = None) -> str:
    """En-tête Authorization d'un envoi : `vapid t=<JWT ES256>, k=<clé publique>`."""
    parts = urlsplit(endpoint)
    claims = {"aud": f"{parts.scheme}://{parts.netloc}", "exp": int((now or time.time()) + JWT_LIFETIME_S),
              "sub": subject}
    token = jwt.encode(claims, private_key, algorithm="ES256", headers={"typ": "JWT"})
    return f"vapid t={token}, k={b64u(public_bytes(private_key.public_key()))}"


def topic(key: str) -> str:
    """En-tête Topic (RFC 8030 § 5.4) : 32 caractères base64url au plus ; un message en attente de même
    sujet est remplacé (une alerte puis son retour à OK ne livrent que le dernier)."""
    return b64u(hashlib.sha256(key.encode("utf-8")).digest())[:32]
