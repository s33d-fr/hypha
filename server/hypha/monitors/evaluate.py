"""Calcul des états de moniteur, sans accès à la base : seuils, retour à la normale, absence de données,
transitions et relances, et les règles du § 17 (tolérances, battement, suppressions, acquittement,
escalades). Le worker fournit les valeurs mesurées et le contexte, ce module décide.

États : OK, WARN, ALERT, NODATA. Gravité pour l'état global : ALERT > WARN > NODATA > OK.
"""
from dataclasses import dataclass, field
from datetime import datetime

from ..util import parse_group_key
from . import template

OK, WARN, ALERT, NODATA = "OK", "WARN", "ALERT", "NODATA"
SEVERITY = {OK: 0, NODATA: 1, WARN: 2, ALERT: 3}
# Contrôles que l'agent n'émet que pour signaler une panne (unité en échec, service arrêté).
PROBLEM_ONLY_CHECKS = frozenset({"systemd.unit", "windows.service"})
EVENT_ALERT_TYPE = {OK: "success", WARN: "warning", ALERT: "error", NODATA: "warning"}
DOWNTIME, DEPENDENCY, FLAPPING = "downtime", "dependency", "flapping"


def breach(value: float, threshold: float, comparator: str) -> bool:
    if comparator == ">":
        return value > threshold
    if comparator == ">=":
        return value >= threshold
    if comparator == "<":
        return value < threshold
    if comparator == "<=":
        return value <= threshold
    raise ValueError(comparator)


def threshold_state(prev: str | None, value: float, thresholds: dict, comparator: str) -> str:
    """État d'après les seuils, avec hystérésis.

    Une alerte ne retombe que quand la valeur ne franchit plus `critical_recovery` (même sens de
    comparaison) ; sans seuil de retour, elle retombe dès que `critical` n'est plus franchi.
    Même règle pour WARN avec `warning_recovery`.
    """
    crit = thresholds.get("critical")
    warn = thresholds.get("warning")
    crit_hit = crit is not None and breach(value, crit, comparator)
    if not crit_hit and prev == ALERT and thresholds.get("critical_recovery") is not None:
        crit_hit = breach(value, thresholds["critical_recovery"], comparator)
    if crit_hit:
        return ALERT
    warn_hit = warn is not None and breach(value, warn, comparator)
    if not warn_hit and prev in (WARN, ALERT) and warn is not None and thresholds.get("warning_recovery") is not None:
        warn_hit = breach(value, thresholds["warning_recovery"], comparator)
    return WARN if warn_hit else OK


def check_status_state(status: int) -> str:
    """Statut d'un contrôle (0 OK, 1 WARN, 2 CRIT, 3 UNKNOWN) ; UNKNOWN compte comme WARN."""
    return {0: OK, 1: WARN, 2: ALERT}.get(status, WARN)


def worst_check_status(statuses: list[int]) -> int:
    rank = {2: 3, 1: 2, 3: 1, 0: 0}
    return max(statuses, key=lambda s: rank.get(s, 1))


def synthetic_window(no_data_s: int, interval_s: int | None) -> int:
    """Fenêtre de lecture des résultats d'un test synthétique : au moins deux passages et une minute.

    Un test lent (DNS de messagerie toutes les 6 h) n'a qu'un résultat par intervalle : lu sur
    `no_data_s` seul (10 min par défaut), son moniteur resterait muet entre deux passages.
    """
    return max(no_data_s, 2 * (interval_s or 0) + 60)


def consecutive_failures(results_newest_first: list[bool]) -> int:
    n = 0
    for ok in results_newest_first:
        if ok:
            break
        n += 1
    return n


def worst(states) -> str:
    states = list(states)
    if not states:
        return NODATA
    return max(states, key=lambda s: SEVERITY.get(s, 0))


def format_value(value) -> str:
    if value is None:
        return "—"
    if isinstance(value, float):
        return f"{value:.4g}"
    return str(value)


def render(template_text: str, ctx: dict) -> str:
    return template.render(template_text, ctx)


DEFAULT_MESSAGE = "{{name}} : {{state}} ({{group}}), valeur {{value}}"


# ── options du § 17 ────────────────────────────────────────────────────────────

@dataclass
class Options:
    require_consecutive: int = 1
    recovery_consecutive: int = 1
    flap_changes: int = 5               # 0 = détection du battement coupée
    flap_window_s: int = 1800
    escalations: list = field(default_factory=list)   # [{"after_s","channel_ids","message"}], par after_s
    depends_on_host: bool = True
    depends_on: list = field(default_factory=list)
    evaluation_delay_s: int = 0

    @classmethod
    def of(cls, options: dict | None) -> "Options":
        o = options or {}
        flap = o.get("flapping", {"changes": 5, "window_s": 1800})
        if flap is False or flap is None:
            flap = {"changes": 0, "window_s": 1800}
        return cls(require_consecutive=int(o.get("require_consecutive") or 1),
                   recovery_consecutive=int(o.get("recovery_consecutive") or 1),
                   flap_changes=int(flap.get("changes", 5)), flap_window_s=int(flap.get("window_s", 1800)),
                   escalations=sorted(o.get("escalations") or [], key=lambda e: e.get("after_s", 0)),
                   depends_on_host=o.get("depends_on_host", True) is not False,
                   depends_on=list(o.get("depends_on") or []),
                   evaluation_delay_s=int(o.get("evaluation_delay_s") or 0))


@dataclass
class PrevState:
    state: str
    value: float | None
    since: datetime
    last_eval: datetime          # dernière évaluation AVEC données (voir plan())
    last_notified: datetime | None
    acked: bool = False
    flapping: bool = False
    escalation_step: int = 0
    suppressed: str | None = None


@dataclass
class Plan:
    upserts: list = field(default_factory=list)       # (group, state, value, since, last_eval, last_notified)
    deletes: list = field(default_factory=list)       # groupes à oublier
    transitions: list = field(default_factory=list)   # (group, from_state, to_state, value, notify: bool)
    renotify: list = field(default_factory=list)      # (group, state, value)
    flags: dict = field(default_factory=dict)         # groupe → (flapping, escalation_step, suppressed, clear_ack)
    escalations: list = field(default_factory=list)   # (group, state, value, marche 1…, définition)
    notices: list = field(default_factory=list)       # (group, state, value, motif) : flapping | flapping_end | suppression_end
    streaks: dict = field(default_factory=dict)       # groupe → (état visé, évaluations consécutives)
    suppressed: dict = field(default_factory=dict)    # groupe → motif de suppression des transitions


def recovered_groups(prev: dict[str, PrevState], measured: dict, alive_hosts: set[str]) -> list[str]:
    """Groupes hors OK qu'aucun check ne cite plus alors que leur hôte émet encore.

    L'agent ne rapporte une unité systemd ou un service Windows qu'en échec, puis un seul cycle
    après : un hôte vivant qui ne cite plus le groupe l'a vu se rétablir. Sans cette règle,
    l'alerte reste figée sur le dernier état lu, puis s'efface sans retour à la normale notifié.
    """
    return [g for g, old in prev.items()
            if g not in measured and old.state != OK and parse_group_key(g).get("host") in alive_hosts]


def _hold(old: PrevState | None, raw: str, streaks: dict, group: str, opts: Options, out: Plan) -> str:
    """Tolérances : l'état ne change qu'après N évaluations consécutives qui le confirment."""
    cur = old.state if old else OK
    if raw == cur or raw == NODATA:
        return raw
    need = opts.recovery_consecutive if raw == OK else opts.require_consecutive
    target, n = streaks.get(group, (None, 0))
    n = n + 1 if target == raw else 1
    if n < need:
        out.streaks[group] = (raw, n)
        return cur
    return raw


def plan(prev: dict[str, PrevState], measured: dict[str, tuple[str, float | None] | None], now: datetime,
         no_data_s: int, notify_no_data: bool, renotify_s: int, muted: bool, *,
         opts: Options | None = None, suppress: dict[str, str] | None = None,
         recent_changes: dict[str, list[datetime]] | None = None, streaks: dict | None = None,
         out_since: dict[str, datetime] | None = None) -> Plan:
    """Décide pour chaque groupe.

    `measured[g]` vaut (état, valeur) quand le groupe a des données, None quand il n'en a pas ;
    un groupe connu absent de `measured` est traité comme sans données. `last_eval` n'avance
    qu'avec des données : il mesure donc depuis quand un groupe est muet.

    `suppress[g]` : motif de suppression venu du worker (`downtime`, `dependency`) ; l'état est
    calculé et enregistré, rien ne part. `recent_changes[g]` : instants des changements d'état sur
    la fenêtre de battement (avant cette évaluation). `streaks` : compteurs des tolérances (mémoire
    du worker). `out_since[g]` : passage hors OK, base des escalades (à défaut, `since`).
    """
    opts = opts or Options()
    suppress = suppress or {}
    recent_changes = recent_changes or {}
    streaks = streaks or {}
    out_since = out_since or {}
    out = Plan()
    for group in set(prev) | set(measured):
        old = prev.get(group)
        m = measured.get(group)
        if m is None:
            if old is None:
                continue
            silent_s = (now - old.last_eval).total_seconds()
            if silent_s < no_data_s or old.state == NODATA:
                continue
            if not notify_no_data:
                # Groupe disparu (hôte retiré, disque démonté) : on l'oublie sans bruit.
                out.deletes.append(group)
                continue
            new_state, value = NODATA, old.value
            last_eval = old.last_eval
        else:
            new_state, value = m
            new_state = _hold(old, new_state, streaks, group, opts, out)
            last_eval = now
        from_state = old.state if old else OK
        changed_state = old is None or new_state != old.state
        transition = changed_state and (old is not None or new_state != OK)

        # Battement : changements sur la fenêtre, celui de cette évaluation compris.
        was_flapping = bool(old and old.flapping)
        changes = len(recent_changes.get(group, [])) + (1 if transition else 0)
        flapping = was_flapping
        if opts.flap_changes > 0:
            if not was_flapping and changes >= opts.flap_changes:
                flapping = True
            elif was_flapping and changes == 0:
                flapping = False
        else:
            flapping = False

        reason = suppress.get(group) or (FLAPPING if flapping else None)
        silent = muted or reason is not None
        acked = bool(old and old.acked) and new_state != OK

        if changed_state:
            notify = transition and not silent
            since = now
            last_notified = now if notify else (old.last_notified if old else None)
        else:
            notify = False
            since = old.since
            last_notified = old.last_notified

        if transition:
            out.transitions.append((group, from_state, new_state, value, notify))
            if reason:
                out.suppressed[group] = reason
        if flapping and not was_flapping and not muted and not suppress.get(group):
            out.notices.append((group, new_state, value, "flapping"))
            last_notified = now
        elif was_flapping and not flapping and not muted and not suppress.get(group) and not transition:
            out.notices.append((group, new_state, value, "flapping_end"))
            last_notified = now
        elif (old is not None and old.suppressed in (DOWNTIME, DEPENDENCY) and not reason and not muted
              and not transition and new_state != OK):
            # Fin de maintenance (ou de la panne de l'hôte) : un groupe toujours hors OK notifie une fois.
            out.notices.append((group, new_state, value, "suppression_end"))
            last_notified = now

        # Relance : seulement un état stable, non acquitté, non supprimé.
        if (not changed_state and new_state != OK and renotify_s > 0 and not silent and not acked
                and not any(n[0] == group for n in out.notices)
                and (now - (old.last_notified or old.since)).total_seconds() >= renotify_s):
            out.renotify.append((group, new_state, value))
            last_notified = now

        # Escalades : marches franchies depuis le passage hors OK, une notification par marche.
        step = (old.escalation_step if old else 0) if new_state != OK else 0
        if new_state != OK and opts.escalations and not silent and not acked:
            ref = now if (old is None or from_state == OK) else out_since.get(group, old.since)
            elapsed = (now - ref).total_seconds()
            for i, esc in enumerate(opts.escalations, start=1):
                if i > step and elapsed >= esc.get("after_s", 0):
                    out.escalations.append((group, new_state, value, i, esc))
                    step = i
        out.upserts.append((group, new_state, value, since, last_eval, last_notified))
        out.flags[group] = (flapping, step, reason, bool(old and old.acked) and new_state == OK)
    return out
