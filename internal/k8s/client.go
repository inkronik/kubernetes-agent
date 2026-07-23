package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

type Client struct {
	Kube    kubernetes.Interface
	Metrics metricsclient.Interface
}

type NodeStatsSummary struct {
	Node NodeStats  `json:"node"`
	Pods []PodStats `json:"pods"`
}

type NodeStats struct {
	Fs      NodeFsStats  `json:"fs"`
	Network NetworkStats `json:"network"`
}

type NodeFsStats struct {
	AvailableBytes uint64 `json:"availableBytes"`
	CapacityBytes  uint64 `json:"capacityBytes"`
	UsedBytes      uint64 `json:"usedBytes"`
	Inodes         uint64 `json:"inodes"`
	InodesFree     uint64 `json:"inodesFree"`
	InodesUsed     uint64 `json:"inodesUsed"`
}

type PodStats struct {
	PodRef  PodReference `json:"podRef"`
	Network NetworkStats `json:"network"`
}

type PodReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid"`
}

type NetworkStats struct {
	Time       string                  `json:"time"`
	Name       string                  `json:"name"`
	RxBytes    uint64                  `json:"rxBytes"`
	RxErrors   uint64                  `json:"rxErrors"`
	TxBytes    uint64                  `json:"txBytes"`
	TxErrors   uint64                  `json:"txErrors"`
	Interfaces []NetworkInterfaceStats `json:"interfaces"`
}

type NetworkInterfaceStats struct {
	Name     string `json:"name"`
	RxBytes  uint64 `json:"rxBytes"`
	RxErrors uint64 `json:"rxErrors"`
	TxBytes  uint64 `json:"txBytes"`
	TxErrors uint64 `json:"txErrors"`
}

func New(kubeconfigPath string) (*Client, error) {
	restConfig, err := buildConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	restConfig.QPS = 10
	restConfig.Burst = 20

	kubeClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	metricsClient, err := metricsclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create metrics client: %w", err)
	}

	return &Client{
		Kube:    kubeClient,
		Metrics: metricsClient,
	}, nil
}

func (c *Client) NodeStatsSummary(ctx context.Context, nodeName string) (summary *NodeStatsSummary, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			summary = nil
			err = fmt.Errorf("get node stats summary: %v", recovered)
		}
	}()

	restClient := c.Kube.CoreV1().RESTClient()
	if restClient == nil {
		return nil, fmt.Errorf("kubernetes core rest client is unavailable")
	}

	raw, err := restClient.Get().Resource("nodes").Name(nodeName).SubResource("proxy").Suffix("stats", "summary").DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("get node stats summary: %w", err)
	}

	var decoded NodeStatsSummary
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode node stats summary: %w", err)
	}

	return &decoded, nil
}

func buildConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	}

	inClusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return inClusterConfig, nil
	}

	return clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
}
