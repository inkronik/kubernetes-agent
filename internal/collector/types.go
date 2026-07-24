package collector

import "github.com/inkronik/kubernetes-agent/internal/k8s"

type ClusterMetadata struct {
	Name         string
	Provider     string
	Environment  string
	AgentVersion string
}

type Options struct {
	Client             *k8s.Client
	Cluster            ClusterMetadata
	Namespaces         []string
	EventTypes         []string
	EnableKubeletStats bool
}
