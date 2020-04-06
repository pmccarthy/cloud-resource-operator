package azure

import (
	"context"

	"github.com/integr8ly/cloud-resource-operator/pkg/resources"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultConfigMapName      = "cloud-provider-config"
	DefaultConfigMapNamespace = "openshift-config"
	DefaultFinalizer          = "finalizers.cloud-resources-operator.integreatly.org"
)

var _ ConfigManager = &ConfigMapConfigManager{}

type ConfigManager interface {
	GetClusterConfig(ctx context.Context) (*v1.ConfigMap, error)
}

type ConfigMapConfigManager struct {
	configMapName      string
	configMapNamespace string
	client             client.Client
}

func NewDefaultConfigMapConfigManager(client client.Client) *ConfigMapConfigManager {
	return NewConfigMapConfigManager(DefaultConfigMapName, DefaultConfigMapNamespace, client)
}

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

func (m *ConfigMapConfigManager) buildDefaultConfigMap() *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: controllerruntime.ObjectMeta{
			Name:      m.configMapName,
			Namespace: m.configMapNamespace,
		},
	}
}

func (m *ConfigMapConfigManager) GetClusterConfig(ctx context.Context) (*v1.ConfigMap, error) {
	cm, err := resources.GetConfigMapOrDefault(ctx, m.client, types.NamespacedName{Name: m.configMapName, Namespace: m.configMapNamespace}, m.buildDefaultConfigMap())
	return cm, err
}
