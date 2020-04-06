package azure

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

var _ AuthManager = &AuthCredentials{}

type AuthManager interface {
	AuthEnvVars(ctx context.Context, config *v1.ConfigMap) []error
}

type AuthCredentials struct {
	TenantID        string
	AadClientID     string
	AadClientSecret string
	SubscriptionID  string
	Logger          *logrus.Entry
	Errors          []error
}

func NewDefaultAuthManager() *AuthCredentials{
	return &AuthCredentials{}
}

func (a *AuthCredentials) AuthEnvVars(ctx context.Context, config *v1.ConfigMap) []error {
	// Parse configmap
	json.Unmarshal([]byte(config.Data["config"]), &a)
	// Array of required environment variables for auth
	envVars := [...]string{"AZURE_SUBSCRIPTION_ID", "AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET"}
	// Map environment variable names to parsed config data
	m := make(map[string]string)
	m["AZURE_SUBSCRIPTION_ID"] = a.SubscriptionID
	m["AZURE_TENANT_ID"] = a.TenantID
	m["AZURE_CLIENT_ID"] = a.AadClientID
	m["AZURE_CLIENT_SECRET"] = a.AadClientSecret
	// Set environment variables
	for _, s := range envVars {
		err := os.Setenv(s, m[s])
		if err != nil {
			a.Errors = append(a.Errors, errors.New(s))
		}
	}
	return a.Errors
}
