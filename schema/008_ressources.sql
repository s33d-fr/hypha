-- HYPHA 0.4.5 (§ 34, § 35) : ressources lues par un connecteur (serveur dédié OVH, hôte ESXi, VM
-- vSphere), hôtes sans agent rattachés à l'agent qui les interroge, comme un équipement SNMP.
ALTER TABLE hypha.hosts DROP CONSTRAINT IF EXISTS hosts_kind_check;
ALTER TABLE hypha.hosts ADD CONSTRAINT hosts_kind_check CHECK (kind IN ('agent','network_device','cloud_resource'));
