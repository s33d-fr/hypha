package config

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Instances d'une intégration (§ 50) : plusieurs cibles pour un même connecteur ou un même
// produit (deux grappes Proxmox, deux comptes AWS, trois serveurs Redis). Chaque entrée de
// « instances » est une conf complète de l'intégration, décodée par-dessus ses valeurs par
// défaut et activée (enabled: true) sauf mention contraire ; son nom (name, obligatoire) devient
// l'étiquette instance_name de ses mesures, contrôles et ressources. L'entrée principale reste
// active comme avant (détection locale en mode auto) ; nommée, elle porte aussi l'étiquette.
// Les listes « instances » de plusieurs fichiers conf.d s'additionnent (merge.go).

// Multi : champs communs aux intégrations qui acceptent des instances.
type Multi struct {
	Name string      `yaml:"name,omitempty"`
	Raw  []yaml.Node `yaml:"instances,omitempty"`
	idx  int         // rang dans « instances » (messages de Validate)
}

// InstanceLists : instances décodées, par intégration.
type InstanceLists struct {
	OVHCloud   []OVHCloudConfig
	MySQL      []MySQLConfig
	Redis      []RedisConfig
	Memcached  []MemcachedConfig
	RabbitMQ   []RabbitMQConfig
	MongoDB    []MongoDBConfig
	OpenSearch []OpenSearchConfig
	Kubernetes []KubernetesConfig
	Libvirt    []LibvirtConfig
	Proxmox    []ProxmoxConfig
	AWS        []AWSConfig
	Scaleway   []ScalewayConfig
	Azure      []AzureConfig
	GCP        []GCPConfig
}

// InstanceTag : clé de l'étiquette qui porte le nom de l'instance (« instance » désigne déjà
// l'adresse du serveur dans les intégrations de produits, à la manière de Datadog).
const InstanceTag = "instance_name"

var instanceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{0,31}$`)

func (c *Config) normalizeInstances() {
	in := &c.Integrations
	d := func() IntegrationsConfig { return Default().Integrations }
	warn := func(s string) { c.Warnings = append(c.Warnings, s) }
	fail := func(s string) { c.instErrs = append(c.instErrs, s) }
	x := &in.Extra
	x.OVHCloud = decodeInstances("ovhcloud", in.OVHCloud.Raw, func() OVHCloudConfig { v := d().OVHCloud; v.Enabled = "true"; return v },
		func(v *OVHCloudConfig) *Multi { return &v.Multi }, warn, fail)
	x.MySQL = decodeInstances("mysql", in.MySQL.Raw, func() MySQLConfig { v := d().MySQL; v.Enabled = "true"; return v },
		func(v *MySQLConfig) *Multi { return &v.Multi }, warn, fail)
	x.Redis = decodeInstances("redis", in.Redis.Raw, func() RedisConfig { v := d().Redis; v.Enabled = "true"; return v },
		func(v *RedisConfig) *Multi { return &v.Multi }, warn, fail)
	x.Memcached = decodeInstances("memcached", in.Memcached.Raw, func() MemcachedConfig { v := d().Memcached; v.Enabled = "true"; return v },
		func(v *MemcachedConfig) *Multi { return &v.Multi }, warn, fail)
	x.RabbitMQ = decodeInstances("rabbitmq", in.RabbitMQ.Raw, func() RabbitMQConfig { v := d().RabbitMQ; v.Enabled = "true"; return v },
		func(v *RabbitMQConfig) *Multi { return &v.Multi }, warn, fail)
	x.MongoDB = decodeInstances("mongodb", in.MongoDB.Raw, func() MongoDBConfig { v := d().MongoDB; v.Enabled = "true"; return v },
		func(v *MongoDBConfig) *Multi { return &v.Multi }, warn, fail)
	x.OpenSearch = decodeInstances("opensearch", in.OpenSearch.Raw, func() OpenSearchConfig { v := d().OpenSearch; v.Enabled = "true"; return v },
		func(v *OpenSearchConfig) *Multi { return &v.Multi }, warn, fail)
	x.Kubernetes = decodeInstances("kubernetes", in.Kubernetes.Raw, func() KubernetesConfig { v := d().Kubernetes; v.Enabled = "true"; return v },
		func(v *KubernetesConfig) *Multi { return &v.Multi }, warn, fail)
	x.Libvirt = decodeInstances("libvirt", in.Libvirt.Raw, func() LibvirtConfig { v := d().Libvirt; v.Enabled = "true"; return v },
		func(v *LibvirtConfig) *Multi { return &v.Multi }, warn, fail)
	x.Proxmox = decodeInstances("proxmox", in.Proxmox.Raw, func() ProxmoxConfig { v := d().Proxmox; v.Enabled = "true"; return v },
		func(v *ProxmoxConfig) *Multi { return &v.Multi }, warn, fail)
	x.AWS = decodeInstances("aws", in.AWS.Raw, func() AWSConfig { v := d().AWS; v.Enabled = "true"; return v },
		func(v *AWSConfig) *Multi { return &v.Multi }, warn, fail)
	x.Scaleway = decodeInstances("scaleway", in.Scaleway.Raw, func() ScalewayConfig { v := d().Scaleway; v.Enabled = "true"; return v },
		func(v *ScalewayConfig) *Multi { return &v.Multi }, warn, fail)
	x.Azure = decodeInstances("azure", in.Azure.Raw, func() AzureConfig { v := d().Azure; v.Enabled = "true"; return v },
		func(v *AzureConfig) *Multi { return &v.Multi }, warn, fail)
	x.GCP = decodeInstances("gcp", in.GCP.Raw, func() GCPConfig { v := d().GCP; v.Enabled = "true"; return v },
		func(v *GCPConfig) *Multi { return &v.Multi }, warn, fail)
}

// decodeInstances décode chaque entrée par-dessus def() ; une clé inconnue devient un
// avertissement (comme ailleurs dans la conf), une entrée illisible une erreur de Validate.
func decodeInstances[T any](key string, raw []yaml.Node, def func() T, multi func(*T) *Multi, warn, fail func(string)) []T {
	var out []T
	for i := range raw {
		at := fmt.Sprintf("integrations.%s.instances[%d]", key, i)
		n := raw[i]
		if n.Kind != yaml.MappingNode {
			fail(at + " : un objet (clé: valeur) est attendu")
			continue
		}
		data, err := yaml.Marshal(&n)
		if err != nil {
			fail(at + " : " + err.Error())
			continue
		}
		v := def()
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&v); err != nil {
			if !strings.Contains(err.Error(), "not found in type") {
				fail(at + " : " + strings.TrimPrefix(err.Error(), "yaml: "))
				continue
			}
			warn(at + " : clé inconnue ignorée : " + strings.TrimPrefix(err.Error(), "yaml: "))
			v = def()
			if err := yaml.Unmarshal(data, &v); err != nil {
				fail(at + " : " + strings.TrimPrefix(err.Error(), "yaml: "))
				continue
			}
		}
		m := multi(&v)
		if len(m.Raw) > 0 {
			fail(at + " : une instance ne peut pas avoir ses propres instances")
			continue
		}
		m.idx = i
		out = append(out, v)
	}
	return out
}

func (c *Config) validateInstances(add, warn func(string, ...any)) {
	for _, e := range c.instErrs {
		add("%s", e)
	}
	in, x := c.Integrations, c.Integrations.Extra
	checkInstances(add, warn, "ovhcloud", in.OVHCloud.Name, x.OVHCloud, func(v *OVHCloudConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v OVHCloudConfig) { s.OVHCloud = v })
	checkInstances(add, warn, "mysql", in.MySQL.Name, x.MySQL, func(v *MySQLConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v MySQLConfig) { s.MySQL = v })
	checkInstances(add, warn, "redis", in.Redis.Name, x.Redis, func(v *RedisConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v RedisConfig) { s.Redis = v })
	checkInstances(add, warn, "memcached", in.Memcached.Name, x.Memcached, func(v *MemcachedConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v MemcachedConfig) { s.Memcached = v })
	checkInstances(add, warn, "rabbitmq", in.RabbitMQ.Name, x.RabbitMQ, func(v *RabbitMQConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v RabbitMQConfig) { s.RabbitMQ = v })
	checkInstances(add, warn, "mongodb", in.MongoDB.Name, x.MongoDB, func(v *MongoDBConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v MongoDBConfig) { s.MongoDB = v })
	checkInstances(add, warn, "opensearch", in.OpenSearch.Name, x.OpenSearch, func(v *OpenSearchConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v OpenSearchConfig) { s.OpenSearch = v })
	checkInstances(add, warn, "kubernetes", in.Kubernetes.Name, x.Kubernetes, func(v *KubernetesConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v KubernetesConfig) { s.Kubernetes = v })
	checkInstances(add, warn, "libvirt", in.Libvirt.Name, x.Libvirt, func(v *LibvirtConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v LibvirtConfig) { s.Libvirt = v })
	checkInstances(add, warn, "proxmox", in.Proxmox.Name, x.Proxmox, func(v *ProxmoxConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v ProxmoxConfig) { s.Proxmox = v })
	checkInstances(add, warn, "aws", in.AWS.Name, x.AWS, func(v *AWSConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v AWSConfig) { s.AWS = v })
	checkInstances(add, warn, "scaleway", in.Scaleway.Name, x.Scaleway, func(v *ScalewayConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v ScalewayConfig) { s.Scaleway = v })
	checkInstances(add, warn, "azure", in.Azure.Name, x.Azure, func(v *AzureConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v AzureConfig) { s.Azure = v })
	checkInstances(add, warn, "gcp", in.GCP.Name, x.GCP, func(v *GCPConfig) *Multi { return &v.Multi },
		func(s *IntegrationsConfig, v GCPConfig) { s.GCP = v })
}

// checkInstances : noms valides et distincts, puis chaque instance passe la validation de
// l'intégration elle-même (dans une conf par défaut), messages renommés à son chemin.
func checkInstances[T any](add, warn func(string, ...any), key, mainName string, list []T, multi func(*T) *Multi,
	put func(*IntegrationsConfig, T)) {
	seen := map[string]bool{}
	if mainName != "" {
		if !instanceNameRe.MatchString(mainName) {
			add("integrations.%s.name invalide : %q (minuscules, chiffres, - et _, 32 au plus)", key, mainName)
		}
		seen[mainName] = true
	}
	prefix := "integrations." + key
	for i := range list {
		m := multi(&list[i])
		name := m.Name
		at := fmt.Sprintf("%s.instances[%d]", prefix, m.idx)
		switch {
		case name == "":
			add("%s : name manquant", at)
		case !instanceNameRe.MatchString(name):
			add("%s : name invalide %q (minuscules, chiffres, - et _, 32 au plus)", at, name)
		case seen[name]:
			add("%s : name %q en double", at, name)
		}
		seen[name] = true
		label := at
		if name != "" {
			label = fmt.Sprintf("%s (%s)", at, name)
		}
		rename := func(f func(string, ...any)) func(string, ...any) {
			return func(format string, a ...any) {
				msg := fmt.Sprintf(format, a...)
				if strings.HasPrefix(msg, prefix+".") || strings.HasPrefix(msg, prefix+" ") {
					f("%s", label+strings.TrimPrefix(msg, prefix))
				}
			}
		}
		s := Default()
		put(&s.Integrations, list[i])
		s.validateV04(rename(add), rename(warn))
	}
}
