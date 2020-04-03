package azure

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/postgresql/mgmt/postgresql"
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
	defaultAzurePostgresSubnet       = "-postgres"
	defaultAzurePostgresSubnetPrefix = ""
	defaultAzurePostgresVnetRuleName = "postgres-vnet-rule"
	azureServiceEndpointSQL          = "Microsoft.Sql"
)

var _ providers.PostgresProvider = &PostgresProvider{}

type PostgresProvider struct {
	Logger        *logrus.Entry
	Client        client.Client
	ConfigManager ConfigManager
	AuthManager   AuthManager
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
		Client:        client,
		Logger:        logger.WithFields(logrus.Fields{"provider": postgresProviderName}),
		ConfigManager: NewDefaultConfigMapConfigManager(client),
		AuthManager:   NewAuthManager(),
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

	// Retrieve Cluster Configuration ConfigMap
	clusterConfigMap, err := p.ConfigManager.getClusterConfig(ctx)
	if err != nil {
		p.Logger.Errorf("Unable to retrieve cluster config map: %v", err)
	}

	// Parse Cluster Configuration ConfigMap
	var clusterConfig ClusterConfig
	json.Unmarshal([]byte(clusterConfigMap.Data["config"]), &clusterConfig)

	// Set required environment variables for authentication
	if err := p.AuthManager.authEnvVars(ctx, clusterConfigMap); err != nil {
		p.Logger.Errorln(err)
	}

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

	// Get postgres client based on set authentication environment variables
	postgresClient, err := getPostgresClient(ctx)
	if err != nil {
		p.Logger.Errorf("Unable to get postgres client: %v", err)
	}

	// Check if Postgres Instance already exists
	foundInstances, err := getAzurePostgresInstances(ctx, postgresClient)
	var postgresHost string
	for i := range *foundInstances.Value {
		if ps.Name == *(*foundInstances.Value)[i].Name {
			p.Logger.Debugf("Found Azure Postgres Instance: %v. Skipping creation", ps.Name)
			// Set Postgres host
			postgresHost = *(*foundInstances.Value)[i].FullyQualifiedDomainName
			break
		}
	}
	// Create Postgres instance if it doesn't already exist
	if postgresHost == "" {
		p.Logger.Debugf("Creating Azure Postgres Instance: %v", ps.Name)
		postgresServer, err := createorUpdateAzurePostgres(ctx, postgresClient, clusterConfig.ResourceGroup, ps.Name, clusterConfig.Location, postgresPass)
		if err != nil {
			p.Logger.Errorf("Error creating or updating postgres instance %v: %v", ps.Name, err)
		}
		postgresHost = *(*postgresServer.ServerProperties).FullyQualifiedDomainName
	}

	// Get subnets client based on set authentication environment variables
	subnetsClient, err := getSubnetsClient(ctx)
	if err != nil {
		p.Logger.Errorf("Unable to get subnet client: %v", err)
	}

	// Retrieve and update worker node subnet for database access
	workerNodeSubnetConfig, err := getAzureSubnetConfig(ctx, subnetsClient, clusterConfig.VnetResourceGroup, clusterConfig.VnetName, clusterConfig.SubnetName)
	if err != nil {
		p.Logger.Errorf("Error retrieving worker node subnet configurations %v", err)
	}

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

	workerNodeSubnetName := *workerNodeSubnetConfig.Name
	workerNodeSubnetID := *workerNodeSubnetConfig.ID
	// workerNodeSecurityGroupID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkSecurityGroups/%s", os.Getenv("AZURE_SUBSCRIPTION_ID"), clusterConfig.ResourceGroup, clusterConfig.SecurityGroupName)

	// Set postgres config variables
	// postgresSubnetName := clusterConfig.SubnetName + defaultAzurePostgresSubnet
	// Takes worker node subnet CIDR range and increments by a count to generate new Postgres subnet
	// For example, worker node subnet CIDR range of 10.40.0.212/24 will generate postgres subnet CIDR range of 10.41.0.212/24 when count is set to 1
	// postgresSubnetCidr, err := incrementCidrAddress(workerNodeSubnetAddressPrefix, 3)
	// if err != nil {
	// 	p.Logger.Errorf("Unable to set postgres CIDR range: %v", err)
	// }

	// Create or update dedicated postgres subnet
	_, err = createOrUpdateAzureSubnet(ctx, subnetsClient, clusterConfig.VnetResourceGroup, clusterConfig.VnetName, workerNodeSubnetName, workerNodeSubnetConfig)
	if err != nil {
		p.Logger.Errorf("Error creating or updating postgres subnet configuration: %v", err)
	}

	// // Set postgres subnet ID
	// postgresSubnetID := *postgresSubnet.ID

	// Update postgres vnet rules to allow generated postgres subnet through to Postgres instance
	vnetRulesClient, err := getVnetRulesClient(ctx)
	if err != nil {
		p.Logger.Errorf("Unable to get vnet rules client: %v", err)
	}

	vnetRule, err := createOrUpdateVnetRules(ctx, vnetRulesClient, clusterConfig.ResourceGroup, ps.Name, defaultAzurePostgresVnetRuleName, workerNodeSubnetID)
	if err != nil {
		p.Logger.Errorf("Error creating or updating postgres vnet rule configuration: %v", err)
	}

	p.Logger.Debugf("VNET RULE: %v", vnetRule)

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

func getAzurePostgresInstances(ctx context.Context, client postgresql.ServersClient) (results postgresql.ServerListResult, err error) {
	return client.List(ctx)
}

func createorUpdateAzurePostgres(ctx context.Context, client postgresql.ServersClient, resourceGroupName string, instanceName string, location string, password string) (server postgresql.Server, err error) {
	future, err := client.Create(
		ctx,
		resourceGroupName,
		instanceName,
		postgresql.ServerForCreate{
			Location: &location,
			Sku: &postgresql.Sku{
				Name: to.StringPtr(defaultAzurePostgresSku),
			},
			Properties: &postgresql.ServerPropertiesForDefaultCreate{
				AdministratorLogin:         to.StringPtr(defaultAzurePostgresUser),
				AdministratorLoginPassword: to.StringPtr(password),
				Version:                    defaultAzurePostgresVersion,
				SslEnforcement:             defaultAzurePostgresSslEnabled,
			},
		})
	if err != nil {
		return server, err
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return server, err
	}
	return future.Result(client)
}

func createOrUpdateAzureSubnet(ctx context.Context, client network.SubnetsClient, resourceGroupName string, vnetName string, subnetName string, subnetConfig network.Subnet) (subnet network.Subnet, err error) {
	future, err := client.CreateOrUpdate(ctx, resourceGroupName, vnetName, subnetName, subnetConfig)
	// network.Subnet{
	// 	SubnetPropertiesFormat: &network.SubnetPropertiesFormat{
	// 		AddressPrefix: to.StringPtr(addressPrefix),
	// 		NetworkSecurityGroup: &network.SecurityGroup{
	// 			Name: to.StringPtr(securityGroup),
	// 			ID:   to.StringPtr(securityGroupID),
	// 		},
	// 		ServiceEndpoints: &[]network.ServiceEndpointPropertiesFormat{
	// 			{
	// 				Service: to.StringPtr("Microsoft.ContainerRegistry"),
	// 			},
	// 			{
	// 				Service: to.StringPtr("Microsoft.Sql"),
	// 			},
	// 		},
	// 	},
	// })
	if err != nil {
		return subnet, fmt.Errorf("cannot create or update subnet: %v", err)
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return subnet, fmt.Errorf("cannot get the subnet create or update future response: %v", err)
	}

	return future.Result(client)
}

func createOrUpdateVnetRules(ctx context.Context, client postgresql.VirtualNetworkRulesClient, resourceGroupName string, serverName string, ruleName string, subnetID string) (result postgresql.VirtualNetworkRule, err error) {
	future, err := client.CreateOrUpdate(
		ctx,
		resourceGroupName,
		serverName,
		ruleName,
		postgresql.VirtualNetworkRule{
			Name: to.StringPtr(ruleName),
			VirtualNetworkRuleProperties: &postgresql.VirtualNetworkRuleProperties{
				VirtualNetworkSubnetID: to.StringPtr(subnetID),
			},
		},
	)
	if err != nil {
		return result, fmt.Errorf("cannot create or update vnet rule: %v", err)
	}

	err = future.WaitForCompletionRef(ctx, client.Client)
	if err != nil {
		return result, fmt.Errorf("cannot get vnet rule create or update future response: %v", err)
	}

	return future.Result(client)
}

func getAzureSubnetConfig(ctx context.Context, client network.SubnetsClient, resourceGroupName string, vnetName string, subnetName string) (subnet network.Subnet, err error) {
	return client.Get(ctx, resourceGroupName, vnetName, subnetName, "")
}

func incrementCidrAddress(cidr string, count byte) (string, error) {
	// Parse CIDR address range
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return cidr, err
	}
	// Convert address to 4-byte
	ip = ip.To4()
	// Increment second element in array by 1
	ip[1] = ip[1] + count
	// Split address range string
	splitCidr := strings.Split(cidr, ".")
	// Construct new CIDR range with incremented values
	incrementedCidr := fmt.Sprintf("%v.%v.%v.%v", splitCidr[0], ip[1], splitCidr[2], splitCidr[3])

	return incrementedCidr, err
}
