package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/integr8ly/cloud-resource-operator/pkg/apis/integreatly/v1alpha1"
	croType "github.com/integr8ly/cloud-resource-operator/pkg/apis/integreatly/v1alpha1/types"
	"github.com/integr8ly/cloud-resource-operator/pkg/providers"
	"github.com/integr8ly/cloud-resource-operator/pkg/resources"
	errorUtil "github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	defaultCredSecSuffix             = "-azure-postgres-credentials"
	defaultPostgresUserKey           = "user"
	defaultPostgresPasswordKey       = "password"
	postgresProviderName             = "azure-postgres"
	defaultAzurePostgresPort         = 5432
	defaultAzurePostgresUser         = "postgres"
	defaultAzurePostgresDatabase     = "postgres"
	defaultAzurePostgresSslEnabled   = "Disabled"
	defaultAzurePostgresVersion      = "10"
	defaultAzurePostgresSku          = "GP_Gen5_2"
	defaultAzurePostgresVnetRuleName = "postgres-vnet-rule"
	azureServiceEndpointSQL          = "Microsoft.Sql"
)

var _ providers.PostgresProvider = &PostgresProvider{}

type PostgresProvider struct {
	Logger               *logrus.Entry
	Client               client.Client
	ConfigManager        ConfigManager
	AuthManager          AuthManager
	AzureResourceManager AzureResourceManager
}

// Struct used for parsing ARO Cluster Configuration ConfigMap
type ClusterConfig struct {
	Location          string
	ResourceGroup     string
	VnetName          string
	SubnetName        string
	VnetResourceGroup string
	SecurityGroupName string
}

func NewDefaultPostgresProvider(client client.Client, logger *logrus.Entry) *PostgresProvider {
	return &PostgresProvider{
		Client:               client,
		Logger:               logger.WithFields(logrus.Fields{"provider": postgresProviderName}),
		ConfigManager:        NewDefaultConfigMapConfigManager(client),
		AuthManager:          NewDefaultAuthManager(),
		AzureResourceManager: NewDefaultAzureResourceManager(),
	}
}

func (p *PostgresProvider) GetName() string {
	return postgresProviderName
}

func (p *PostgresProvider) SupportsStrategy(s string) bool {
	return s == "azure"
}

func (p *PostgresProvider) GetReconcileTime(ps *v1alpha1.Postgres) time.Duration {
	return resources.GetForcedReconcileTimeOrDefault(time.Second * 30)
}

func (p *PostgresProvider) CreatePostgres(ctx context.Context, ps *v1alpha1.Postgres) (*providers.PostgresInstance, croType.StatusMessage, error) {
	// handle provider-specific finalizer
	if err := resources.CreateFinalizer(ctx, p.Client, ps, DefaultFinalizer); err != nil {
		return nil, "failed to set finalizer", err
	}

	// retrieve cluster configuration configMap
	clusterConfigMap, err := p.ConfigManager.GetClusterConfig(ctx)
	if err != nil {
		p.Logger.Errorf("Unable to retrieve cluster config map: %v", err)
	}

	// parse cluster configuration configMap
	var clusterConfig ClusterConfig
	json.Unmarshal([]byte(clusterConfigMap.Data["config"]), &clusterConfig)

	// set required environment variables for authentication
	if err := p.AuthManager.AuthEnvVars(ctx, clusterConfigMap); err != nil {
		p.Logger.Errorln(err)
	}

	// set azure client authorizer
	authorizer, err := auth.NewAuthorizerFromCLI()
	if err != nil {
		panic(err)
	}

	// get azure resource client
	azureResourceClient := p.AzureResourceManager.NewAzureResourceClient(ctx, os.Getenv("AZURE_SUBSCRIPTION_ID"), authorizer)

	// create credentials secret
	sec := buildDefaultPostgresSecret(ps)
	or, err := controllerutil.CreateOrUpdate(ctx, p.Client, sec, func() error {
		return nil
	})
	if err != nil {
		errMsg := fmt.Sprintf("failed to create or update secret %s, action was %s", sec.Name, or)
		return nil, croType.StatusMessage(errMsg), errorUtil.Wrapf(err, errMsg)
	}

	// retrieve postgres user password from created credentials secret
	credSec := &v1.Secret{}
	if err := p.Client.Get(ctx, types.NamespacedName{Name: ps.Name + defaultCredSecSuffix, Namespace: ps.Namespace}, credSec); err != nil {
		msg := "failed to retrieve rds credential secret"
		return nil, croType.StatusMessage(msg), errorUtil.Wrap(err, msg)
	}
	postgresPass := string(credSec.Data[defaultPostgresPasswordKey])
	if postgresPass == "" {
		msg := "unable to retrieve rds password"
		return nil, croType.StatusMessage(msg), errorUtil.Wrap(err, msg)
	}

	// check if postgres instance already exists
	foundInstances, err := azureResourceClient.GetAzurePostgresInstances(ctx)
	if err != nil{
		p.Logger.Errorf("Error retrieving Azure Postgres Instances %v", err)
	}
	var postgresHost string
	for i := range *foundInstances.Value {
		if ps.Name == *(*foundInstances.Value)[i].Name {
			p.Logger.Debugf("Found Azure Postgres Instance: %v. Skipping creation", ps.Name)
			// Set Postgres host
			postgresHost = *(*foundInstances.Value)[i].FullyQualifiedDomainName
			break
		}
	}

	// create postgres instance if it doesn't already exist
	if postgresHost == "" {
		p.Logger.Debugf("Creating Azure Postgres Instance: %v", ps.Name)
		postgresServer, err := azureResourceClient.CreateorUpdateAzurePostgres(ctx, clusterConfig.ResourceGroup, ps.Name, clusterConfig.Location, postgresPass)
		if err != nil {
			p.Logger.Errorf("Error creating or updating postgres instance %v: %v", ps.Name, err)
		}
		postgresHost = *(*postgresServer.ServerProperties).FullyQualifiedDomainName
	}

	// retrieve and update worker node subnet for database access
	workerNodeSubnetConfig, err := azureResourceClient.GetAzureSubnet(ctx, clusterConfig.VnetResourceGroup, clusterConfig.VnetName, clusterConfig.SubnetName)
	if err != nil {
		p.Logger.Errorf("Error retrieving worker node subnet configurations %v", err)
	}

	// add Microsoft.sql service endpoint to worker node subnet config if not present
	serviceEndpoints := *(*workerNodeSubnetConfig.SubnetPropertiesFormat).ServiceEndpoints
	var foundServiceEndpoint bool
	for i := range serviceEndpoints {
		// p.Logger.Debugf("ENDPOINT %v", *serviceEndpoints[i].Service)
		if *serviceEndpoints[i].Service == azureServiceEndpointSQL {
			foundServiceEndpoint = true
			break
		}
	}
	if !foundServiceEndpoint {
		serviceEndpoints = append(serviceEndpoints, network.ServiceEndpointPropertiesFormat{
			Service: to.StringPtr(azureServiceEndpointSQL),
		})
		*(*workerNodeSubnetConfig.SubnetPropertiesFormat).ServiceEndpoints = serviceEndpoints
	}

	// update worker node subnet with modified configs
	_, err = azureResourceClient.CreateorUpdateAzureSubnet(ctx, clusterConfig.VnetResourceGroup, clusterConfig.VnetName, clusterConfig.SubnetName, workerNodeSubnetConfig)
	if err != nil {
		p.Logger.Errorf("Error creating or updating subnet: %v", err)
	}

	// create postgres virtual network rule to allow access from worker node subnet
	_, err = azureResourceClient.CreateorUpdateAzurePostgresVnetRule(ctx, clusterConfig.ResourceGroup, ps.Name, defaultAzurePostgresVnetRuleName, *workerNodeSubnetConfig.ID)
	if err != nil {
		p.Logger.Errorf("Error creating or updating postgres vnet rule configuration: %v", err)
	}

	// Return postgres instance configurations
	return &providers.PostgresInstance{
		DeploymentDetails: &providers.PostgresDeploymentDetails{
			Username: defaultAzurePostgresUser,
			Password: postgresPass,
			Host:     postgresHost,
			Database: defaultAzurePostgresDatabase,
			Port:     defaultAzurePostgresPort,
		},
	}, "completed", nil
}

func (p *PostgresProvider) DeletePostgres(ctx context.Context, ps *v1alpha1.Postgres) (croType.StatusMessage, error) {
	panic("implement me")
}

func buildDefaultPostgresSecret(ps *v1alpha1.Postgres) *v1.Secret {
	password, err := resources.GeneratePassword()
	if err != nil {
		return nil
	}
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ps.Name + defaultCredSecSuffix,
			Namespace: ps.Namespace,
		},
		StringData: map[string]string{
			defaultPostgresUserKey:     defaultAzurePostgresUser,
			defaultPostgresPasswordKey: password,
		},
		Type: v1.SecretTypeOpaque,
	}
}
