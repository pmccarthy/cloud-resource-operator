package openshift

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/integr8ly/cloud-resource-operator/pkg/providers"
	errorUtil "github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultConfigMapName      = "cloud-resources-openshift-strategies"
	DefaultConfigMapNamespace = "kube-system"
	DefaultFinalizer          = "finalizers.openshift.cloud-resources-operator.integreatly.org"
)

type StrategyConfig struct {
	RawStrategy json.RawMessage `json:"strategy"`
}

//go:generate moq -out config_moq.go . ConfigManager
type ConfigManager interface {
	ReadStorageStrategy(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error)
}

type ConfigMapConfigManager struct {
	configMapName      string
	configMapNamespace string
	client             client.Client
}

var _ ConfigManager = (*ConfigMapConfigManager)(nil)

func NewConfigMapConfigManager(cm string, namespace string, client client.Client) *ConfigMapConfigManager {
	if cm == "" {
		cm = DefaultConfigMapName
	}
	if namespace == "" {
		namespace = DefaultConfigMapNamespace
	}
	return &ConfigMapConfigManager{
		configMapName:      cm,
		configMapNamespace: namespace,
		client:             client,
	}
}

func NewDefaultConfigManager(client client.Client) *ConfigMapConfigManager {
	return NewConfigMapConfigManager(DefaultConfigMapName, DefaultConfigMapNamespace, client)
}

func (m *ConfigMapConfigManager) ReadStorageStrategy(ctx context.Context, rt providers.ResourceType, tier string) (*StrategyConfig, error) {
	cm := &v1.ConfigMap{}
	err := m.client.Get(ctx, types.NamespacedName{Name: m.configMapName, Namespace: m.configMapNamespace}, cm)
	if err != nil {
		return nil, errorUtil.Wrapf(err, "failed to get aws strategy config map %s in namespace %s", m.configMapName, m.configMapNamespace)
	}
	rawStrategyCfg := cm.Data[string(rt)]
	if rawStrategyCfg == "" {
		return nil, errorUtil.New(fmt.Sprintf("aws strategy for resource type %s is not defined", rt))
	}

	var strategies map[string]*StrategyConfig
	if err = json.Unmarshal([]byte(rawStrategyCfg), &strategies); err != nil {
		return nil, errorUtil.Wrapf(err, "failed to unmarshal strategy mapping for resource type %s", rt)
	}
	tierStrat := strategies[tier]
	return tierStrat, nil
}