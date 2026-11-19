"""Catalogue livré des métadonnées de métriques (contrat § 30) : unité et description.

Métriques de l'agent (§ 4, § 14, intégrations PostgreSQL, HAProxy, nginx). Une organisation peut
remplacer une entrée (`metric_metadata`) ; l'entrée de l'organisation passe devant le catalogue.
Une métrique à suffixe variable (`haproxy.backend.response.5xx`) est décrite par son motif.
"""
import re

# métrique → (unité, description, intégration)
CATALOG: dict[str, tuple[str | None, str, str]] = {
    # ── système (§ 4) ──
    "system.cpu.user": ("percent", "Part du processeur passée en mode utilisateur", "system"),
    "system.cpu.system": ("percent", "Part du processeur passée en mode noyau", "system"),
    "system.cpu.idle": ("percent", "Part du processeur au repos", "system"),
    "system.cpu.usage": ("percent", "Utilisation du processeur (100 − repos)", "system"),
    "system.cpu.iowait": ("percent", "Part du processeur en attente d'entrées-sorties", "system"),
    "system.cpu.steal": ("percent", "Part du processeur prise par l'hyperviseur", "system"),
    "system.cpu.interrupt": ("percent", "Part du processeur passée en interruptions", "system"),
    "system.cpu.num_cores": ("count", "Nombre de cœurs logiques", "system"),
    "system.load.1": ("count", "Charge moyenne sur 1 minute", "system"),
    "system.load.5": ("count", "Charge moyenne sur 5 minutes", "system"),
    "system.load.15": ("count", "Charge moyenne sur 15 minutes", "system"),
    "system.load.norm.1": ("count", "Charge sur 1 minute divisée par le nombre de cœurs", "system"),
    "system.mem.total": ("byte", "Mémoire physique totale", "system"),
    "system.mem.used": ("byte", "Mémoire utilisée", "system"),
    "system.mem.free": ("byte", "Mémoire libre", "system"),
    "system.mem.usable": ("byte", "Mémoire disponible pour les applications", "system"),
    "system.mem.cached": ("byte", "Mémoire en cache de pages", "system"),
    "system.mem.buffered": ("byte", "Mémoire en tampons du noyau", "system"),
    "system.mem.pct_used": ("percent", "Part de la mémoire utilisée", "system"),
    "system.mem.committed": ("byte", "Mémoire engagée (Windows)", "system"),
    "system.mem.commit_limit": ("byte", "Limite d'engagement de la mémoire (Windows)", "system"),
    "system.swap.total": ("byte", "Espace d'échange total", "system"),
    "system.swap.used": ("byte", "Espace d'échange utilisé", "system"),
    "system.swap.free": ("byte", "Espace d'échange libre", "system"),
    "system.swap.pct_used": ("percent", "Part de l'espace d'échange utilisée", "system"),
    "system.disk.total": ("byte", "Taille du système de fichiers", "system"),
    "system.disk.used": ("byte", "Espace utilisé du système de fichiers", "system"),
    "system.disk.free": ("byte", "Espace libre du système de fichiers", "system"),
    "system.disk.in_use": ("percent", "Part du système de fichiers utilisée", "system"),
    "system.fs.inodes.in_use": ("percent", "Part des inœuds utilisés", "system"),
    "system.io.read_bytes": ("byte_per_second", "Débit de lecture du disque", "system"),
    "system.io.write_bytes": ("byte_per_second", "Débit d'écriture du disque", "system"),
    "system.io.r_s": ("operation_per_second", "Lectures par seconde", "system"),
    "system.io.w_s": ("operation_per_second", "Écritures par seconde", "system"),
    "system.io.util": ("percent", "Taux d'occupation du disque", "system"),
    "system.io.await": ("millisecond", "Attente moyenne d'une entrée-sortie", "system"),
    "system.net.bytes_rcvd": ("byte_per_second", "Débit reçu par l'interface", "system"),
    "system.net.bytes_sent": ("byte_per_second", "Débit émis par l'interface", "system"),
    "system.net.packets_in": ("packet_per_second", "Paquets reçus par seconde", "system"),
    "system.net.packets_out": ("packet_per_second", "Paquets émis par seconde", "system"),
    "system.net.errors_in": ("packet_per_second", "Erreurs en réception par seconde", "system"),
    "system.net.errors_out": ("packet_per_second", "Erreurs en émission par seconde", "system"),
    "system.net.drops_in": ("packet_per_second", "Paquets reçus abandonnés par seconde", "system"),
    "system.net.tcp.connections": ("count", "Connexions TCP par état", "system"),
    "system.uptime": ("second", "Temps écoulé depuis le démarrage", "system"),
    "system.processes.count": ("count", "Nombre de processus", "system"),
    "system.processes.running": ("count", "Processus en cours d'exécution", "system"),
    "system.processes.zombie": ("count", "Processus zombies", "system"),
    "system.file_handles.in_use": ("count", "Descripteurs de fichiers ouverts", "system"),
    "system.updates.pending": ("count", "Mises à jour en attente", "system"),
    "system.updates.security": ("count", "Mises à jour de sécurité en attente", "system"),
    "system.reboot_required": ("count", "Redémarrage requis (1) ou non (0)", "system"),
    "system.temperature": ("celsius", "Température d'un capteur", "system"),
    "system.fan.rpm": ("rpm", "Vitesse d'un ventilateur", "system"),
    # ── équipements réseau (§ 14) ──
    "snmp.ifBandwidthIn": ("byte_per_second", "Débit entrant de l'interface (ifHCInOctets)", "snmp"),
    "snmp.ifBandwidthOut": ("byte_per_second", "Débit sortant de l'interface (ifHCOutOctets)", "snmp"),
    "snmp.ifBandwidthInUsage": ("percent", "Occupation entrante de l'interface (÷ ifHighSpeed)", "snmp"),
    "snmp.ifBandwidthOutUsage": ("percent", "Occupation sortante de l'interface (÷ ifHighSpeed)", "snmp"),
    "snmp.ifInErrors": ("packet_per_second", "Erreurs en réception de l'interface", "snmp"),
    "snmp.ifOutErrors": ("packet_per_second", "Erreurs en émission de l'interface", "snmp"),
    "snmp.ifInDiscards": ("packet_per_second", "Paquets reçus abandonnés par l'interface", "snmp"),
    "snmp.ifOutDiscards": ("packet_per_second", "Paquets à émettre abandonnés par l'interface", "snmp"),
    "snmp.ifOperStatus": ("count", "État opérationnel de l'interface (1 en service, 2 hors service)", "snmp"),
    "snmp.ifAdminStatus": ("count", "État administratif de l'interface (1 activée, 2 désactivée)", "snmp"),
    "snmp.sysUpTimeInstance": ("second", "Temps écoulé depuis le démarrage de l'équipement", "snmp"),
    "snmp.cpu.usage": ("percent", "Charge du processeur de l'équipement", "snmp"),
    "snmp.memory.usage": ("percent", "Part de la mémoire utilisée", "snmp"),
    "snmp.memory.total": ("byte", "Mémoire totale de l'équipement", "snmp"),
    "snmp.memory.used": ("byte", "Mémoire utilisée de l'équipement", "snmp"),
    "snmp.storage.usage": ("percent", "Part d'un stockage utilisée", "snmp"),
    "snmp.temperature": ("celsius", "Température d'un capteur de l'équipement", "snmp"),
    "snmp.fan.speed": ("rpm", "Vitesse d'un ventilateur de l'équipement", "snmp"),
    "snmp.psu.status": ("count", "État d'une alimentation", "snmp"),
    "snmp.ups.battery.charge": ("percent", "Charge de la batterie de l'onduleur", "snmp"),
    "snmp.ups.battery.runtime": ("second", "Autonomie restante de l'onduleur", "snmp"),
    "snmp.ups.battery.load": ("percent", "Charge de sortie de l'onduleur", "snmp"),
    "snmp.poll.duration": ("millisecond", "Durée d'une interrogation SNMP", "snmp"),
    "snmp.devices.unreachable": ("count", "Équipements qui ne répondent pas", "snmp"),
    # ── PostgreSQL ──
    "postgresql.connections": ("count", "Connexions ouvertes", "postgresql"),
    "postgresql.max_connections": ("count", "Connexions permises (max_connections)", "postgresql"),
    "postgresql.percent_usage_connections": ("percent", "Part des connexions permises utilisée", "postgresql"),
    "postgresql.commits": ("operation_per_second", "Transactions validées par seconde", "postgresql"),
    "postgresql.rollbacks": ("operation_per_second", "Transactions annulées par seconde", "postgresql"),
    "postgresql.deadlocks": ("operation_per_second", "Interblocages par seconde", "postgresql"),
    "postgresql.buffer_hit": ("percent", "Part des lectures servies par le cache", "postgresql"),
    "postgresql.database_size": ("byte", "Taille de la base", "postgresql"),
    "postgresql.temp_bytes": ("byte_per_second", "Débit d'écriture des fichiers temporaires", "postgresql"),
    "postgresql.rows_fetched": ("operation_per_second", "Lignes lues par seconde", "postgresql"),
    "postgresql.rows_inserted": ("operation_per_second", "Lignes insérées par seconde", "postgresql"),
    "postgresql.rows_updated": ("operation_per_second", "Lignes modifiées par seconde", "postgresql"),
    "postgresql.rows_deleted": ("operation_per_second", "Lignes supprimées par seconde", "postgresql"),
    "postgresql.is_primary": ("count", "Primaire (1) ou réplique (0)", "postgresql"),
    "postgresql.replication_delay": ("second", "Retard de rejeu d'une réplique", "postgresql"),
    "postgresql.replication.wal_lag_bytes": ("byte", "Retard de réplication en octets de WAL", "postgresql"),
    "postgresql.locks": ("count", "Verrous tenus, par mode", "postgresql"),
    "postgresql.waiting_queries": ("count", "Requêtes en attente d'un verrou", "postgresql"),
    "postgresql.longest_query_s": ("second", "Durée de la plus longue requête en cours", "postgresql"),
    "postgresql.idle_in_transaction": ("count", "Connexions inactives dans une transaction ouverte", "postgresql"),
    "postgresql.bgwriter.checkpoints_timed": ("operation_per_second", "Points de contrôle planifiés", "postgresql"),
    "postgresql.bgwriter.checkpoints_requested": ("operation_per_second", "Points de contrôle demandés", "postgresql"),
    "postgresql.bgwriter.buffers_checkpoint": ("operation_per_second", "Tampons écrits aux points de contrôle", "postgresql"),
    "postgresql.bgwriter.buffers_clean": ("operation_per_second", "Tampons écrits par le bgwriter", "postgresql"),
    "postgresql.bgwriter.buffers_backend": ("operation_per_second", "Tampons écrits par les processus serveurs", "postgresql"),
    "postgresql.bgwriter.buffers_alloc": ("operation_per_second", "Tampons alloués", "postgresql"),
    "postgresql.bgwriter.maxwritten_clean": ("operation_per_second", "Arrêts du bgwriter (trop d'écritures)", "postgresql"),
    "postgresql.bgwriter.write_time": ("millisecond", "Temps d'écriture des points de contrôle", "postgresql"),
    "postgresql.bgwriter.sync_time": ("millisecond", "Temps de synchronisation des points de contrôle", "postgresql"),
    # ── HAProxy ──
    "haproxy.server.up": ("count", "Serveur du backend en service (1) ou non (0)", "haproxy"),
    # ── nginx ──
    "nginx.connections.active": ("count", "Connexions actives", "nginx"),
    "nginx.connections.reading": ("count", "Connexions en lecture de la requête", "nginx"),
    "nginx.connections.writing": ("count", "Connexions en écriture de la réponse", "nginx"),
    "nginx.connections.waiting": ("count", "Connexions inactives gardées ouvertes", "nginx"),
    "nginx.requests": ("operation_per_second", "Requêtes par seconde", "nginx"),
}

# Motifs (métriques à préfixe frontend/backend) : (expression, unité, description, intégration).
PATTERNS = [
    (re.compile(r"^haproxy\.(frontend|backend)\.sessions\.current$"), "count", "Sessions en cours", "haproxy"),
    (re.compile(r"^haproxy\.backend\.queue\.current$"), "count", "Requêtes en file d'attente du backend", "haproxy"),
    (re.compile(r"^haproxy\.(frontend|backend)\.bytes\.in$"), "byte_per_second", "Débit entrant", "haproxy"),
    (re.compile(r"^haproxy\.(frontend|backend)\.bytes\.out$"), "byte_per_second", "Débit sortant", "haproxy"),
    (re.compile(r"^haproxy\.(frontend|backend)\.response\.([1-5])xx$"), "operation_per_second",
     "Réponses HTTP de la classe indiquée par seconde", "haproxy"),
    (re.compile(r"^haproxy\.(frontend|backend)\.errors$"), "operation_per_second", "Erreurs par seconde", "haproxy"),
]


def lookup(metric: str) -> dict | None:
    """Entrée du catalogue livré (exacte ou par motif), sinon None."""
    entry = CATALOG.get(metric)
    if entry is not None:
        unit, description, integration = entry
        return {"metric": metric, "unit": unit, "per_unit": None, "description": description,
                "integration": integration, "source": "catalog"}
    for rx, unit, description, integration in PATTERNS:
        if rx.match(metric):
            return {"metric": metric, "unit": unit, "per_unit": None, "description": description,
                    "integration": integration, "source": "catalog"}
    return None
