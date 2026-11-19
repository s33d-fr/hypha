"""Erreurs de l'API au format du contrat : `{"error":"code_court","detail":"phrase lisible"}`."""


class ApiError(Exception):
    def __init__(self, status: int, code: str, detail: str = "", **extra):
        super().__init__(detail or code)
        self.status = status
        self.code = code
        self.detail = detail or code
        self.extra = extra

    def body(self) -> dict:
        return {"error": self.code, "detail": self.detail, **self.extra}


def bad_request(detail: str, code: str = "invalid_request") -> ApiError:
    return ApiError(400, code, detail)


def not_found(detail: str = "objet introuvable") -> ApiError:
    return ApiError(404, "not_found", detail)


def forbidden(detail: str = "droits insuffisants", code: str = "forbidden") -> ApiError:
    return ApiError(403, code, detail)


def unauthenticated(detail: str = "connexion requise", **extra) -> ApiError:
    return ApiError(401, "unauthenticated", detail, **extra)


class QueryError(ValueError):
    """Requête de l'utilisateur (métriques, journaux) mal formée ; le message est montré tel quel."""
