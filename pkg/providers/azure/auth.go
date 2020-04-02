package azure

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/postgresql/mgmt/postgresql"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

// Interface for Azure authentication
type AuthManager interface {
	authEnvVars(ctx context.Context, config *v1.ConfigMap) error
}

type AuthCredentials struct {
	TenantID        string
	AadClientID     string
	AadClientSecret string
	SubscriptionID  string
	Logger          *logrus.Entry
}

var _ AuthManager = (*AuthCredentials)(nil)

func NewAuthManager() *AuthCredentials {
	return &AuthCredentials{}
}

func (a *AuthCredentials) authEnvVars(ctx context.Context, config *v1.ConfigMap) error {
	// Parse configmap
	var authCredentials AuthCredentials
	json.Unmarshal([]byte(config.Data["config"]), &authCredentials)
	// Array of required environment variables for auth
	envVars := [...]string{"AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"}
	// Map environment variable names to parsed config data
	m := make(map[string]string)
	m["AZURE_SUBSCRIPTION_ID"] = authCredentials.SubscriptionID
	m["AZURE_TENANT_ID"] = authCredentials.TenantID
	m["AZURE_CLIENT_ID"] = authCredentials.AadClientID
	m["AZURE_CLIENT_SECRET"] = authCredentials.AadClientSecret
	// Set environment variables
	for _, s := range envVars {
		err := os.Setenv(s, m[s])
		if err != nil {
			a.Logger.Errorf("error setting environment variable for authentication %v: %v", s, err)
		}
	}
	return nil
}

func getPostgresClient(ctx context.Context) (client postgresql.ServersClient, err error) {
	postgresClient := postgresql.NewServersClient(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		postgresClient.Authorizer = authorizer
	}
	return postgresClient, err
}

func getSubnetsClient(ctx context.Context) (client network.SubnetsClient, err error) {
	subnetsClient := network.NewSubnetsClient(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		subnetsClient.Authorizer = authorizer
	}
	return subnetsClient, err
}

func getVnetRulesClient(ctx context.Context) (client postgresql.VirtualNetworkRulesClient, err error) {
	vnetRulesClient := postgresql.NewVirtualNetworkRulesClient(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		vnetRulesClient.Authorizer = authorizer
	}
	return vnetRulesClient, err
}

// func getNsgClient(ctx context.Context) (client network.SecurityGroupsClient, err error) {
// 	nsgClient := network.NewSecurityGroupsClient(os.Getenv("AZURE_SUBSCRIPTION_ID"))
// 	authorizer, err := auth.NewAuthorizerFromEnvironment()
// 	if err == nil {
// 		nsgClient.Authorizer = authorizer
// 	}
// 	return nsgClient, err
// }
