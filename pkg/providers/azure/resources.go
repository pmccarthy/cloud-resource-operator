package azure

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/postgresql/mgmt/postgresql"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
)

var _ AzureResourceManager = &AzureResourceClient{}

type AzureResourceManager interface {
	CreateorUpdateAzurePostgres(ctx context.Context, resourceGroupName string, instanceName string, location string, password string) (postgresql.Server, error)
	CreateorUpdateAzurePostgresVnetRule(ctx context.Context, resourceGroupName string, serverName string, ruleName string, subnetID string) (postgresql.VirtualNetworkRule, error)
	CreateorUpdateAzureSubnet(ctx context.Context, resourceGroupName string, vnetName string, subnetName string, subnetConfig network.Subnet) (network.Subnet, error)
	GetAzurePostgresInstances(ctx context.Context) (postgresql.ServerListResult, error)
	GetAzureSubnet(ctx context.Context, resourceGroupName string, vnetName string, subnetName string) (network.Subnet, error)
	NewAzureResourceClient(ctx context.Context, subscriptionID string, authorizer autorest.Authorizer) *AzureResourceClient
}

type AzureResourceClient struct {
	subscriptionID string
	authorizer     autorest.Authorizer
}

func NewDefaultAzureResourceManager () *AzureResourceClient{
	return &AzureResourceClient{}
}

func (c *AzureResourceClient) CreateorUpdateAzurePostgres(ctx context.Context, resourceGroupName string, instanceName string, location string, password string) (server postgresql.Server, err error) {
	postgresClient := postgresql.NewServersClient(c.subscriptionID)
	postgresClient.Authorizer = c.authorizer

	future, err := postgresClient.Create(
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

	err = future.WaitForCompletionRef(ctx, postgresClient.Client)
	if err != nil {
		return server, err
	}
	return future.Result(postgresClient)
}

func (c *AzureResourceClient) CreateorUpdateAzurePostgresVnetRule(ctx context.Context, resourceGroupName string, serverName string, ruleName string, subnetID string) (rule postgresql.VirtualNetworkRule, err error) {
	vnetRulesClient := postgresql.NewVirtualNetworkRulesClient(c.subscriptionID)
	vnetRulesClient.Authorizer = c.authorizer

	future, err := vnetRulesClient.CreateOrUpdate(
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
		return rule, err
	}

	err = future.WaitForCompletionRef(ctx, vnetRulesClient.Client)
	if err != nil {
		return rule, err
	}

	return future.Result(vnetRulesClient)
}

func (c *AzureResourceClient) CreateorUpdateAzureSubnet(ctx context.Context, resourceGroupName string, vnetName string, subnetName string, subnetConfig network.Subnet) (subnet network.Subnet, err error) {
	subnetClient := network.NewSubnetsClient(c.subscriptionID)
	subnetClient.Authorizer = c.authorizer
	future, err := subnetClient.CreateOrUpdate(ctx, resourceGroupName, vnetName, subnetName, subnetConfig)
	if err != nil {
		return subnet, err
	}

	err = future.WaitForCompletionRef(ctx, subnetClient.Client)
	if err != nil {
		return subnet, err
	}

	return future.Result(subnetClient)
}

func (c *AzureResourceClient) GetAzureSubnet(ctx context.Context, resourceGroupName string, vnetName string, subnetName string) (subnet network.Subnet, err error) {
	subnetClient := network.NewSubnetsClient(c.subscriptionID)
	subnetClient.Authorizer = c.authorizer
	return subnetClient.Get(ctx, resourceGroupName, vnetName, subnetName, "")
}

func (c *AzureResourceClient) GetAzurePostgresInstances(ctx context.Context) (postgresql.ServerListResult, error) {
	postgresClient := postgresql.NewServersClient(c.subscriptionID)
	postgresClient.Authorizer = c.authorizer
	return postgresClient.List(ctx)
}

func (c *AzureResourceClient) NewAzureResourceClient(ctx context.Context, subscriptionID string, authorizer autorest.Authorizer) *AzureResourceClient {
	return &AzureResourceClient{
		subscriptionID: subscriptionID,
		authorizer:     authorizer,
	}
}
