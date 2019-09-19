package smtpcredentialset

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/integr8ly/cloud-resource-operator/pkg/apis/integreatly/v1alpha1"

	"github.com/integr8ly/cloud-resource-operator/pkg/providers"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/types"

	"github.com/integr8ly/cloud-resource-operator/pkg/apis"
	apis2 "github.com/openshift/cloud-credential-operator/pkg/apis"
	v12 "k8s.io/api/core/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func buildTestScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	err := apis2.AddToScheme(scheme)
	err = v12.AddToScheme(scheme)
	err = apis.AddToScheme(scheme)
	if err != nil {
		return nil, err
	}
	return scheme, nil
}

func buildTestOperatorConfigMap() *v12.ConfigMap {
	return &v12.ConfigMap{
		ObjectMeta: controllerruntime.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Data: map[string]string{
			"test": "{ \"smtpcredentials\": \"test\" }",
		},
	}
}

func buildTestSMTPCredentialSet() *v1alpha1.SMTPCredentialSet {
	return &v1alpha1.SMTPCredentialSet{
		ObjectMeta: controllerruntime.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: v1alpha1.SMTPCredentialSetSpec{
			Tier:      "test",
			Type:      "test",
			SecretRef: &v1alpha1.SecretRef{Name: "test"},
		},
	}
}

func TestReconcileSMTPCredentialSet_Reconcile(t *testing.T) {
	scheme, err := buildTestScheme()
	if err != nil {
		t.Error("unexpected error while constructing test scheme", err)
	}

	type fields struct {
		client client.Client
		scheme *runtime.Scheme
		logger *logrus.Entry
	}
	type args struct {
		context       context.Context
		request       reconcile.Request
		providerList  []providers.SMTPCredentialsProvider
		configManager providers.ConfigManager
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    reconcile.Result
		wantErr bool
	}{
		{
			name: "test successful reconcile sets repeated reconciliation to true",
			fields: fields{
				client: fake.NewFakeClientWithScheme(scheme, buildTestOperatorConfigMap(), buildTestSMTPCredentialSet()),
				scheme: scheme,
				logger: logrus.WithFields(logrus.Fields{}),
			},
			args: args{
				context: context.TODO(),
				request: reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: "test",
						Name:      "test",
					},
				},
				providerList: []providers.SMTPCredentialsProvider{
					&providers.SMTPCredentialsProviderMock{
						CreateSMTPCredentialsFunc: func(ctx context.Context, smtpCreds *v1alpha1.SMTPCredentialSet) (instance *providers.SMTPCredentialSetInstance, e error) {
							return &providers.SMTPCredentialSetInstance{
								DeploymentDetails: &providers.DeploymentDetailsMock{
									DataFunc: func() map[string][]byte {
										return map[string][]byte{
											"test": []byte("test"),
										}
									},
								},
							}, nil
						},
						DeleteSMTPCredentialsFunc: func(ctx context.Context, smtpCreds *v1alpha1.SMTPCredentialSet) error {
							return nil
						},
						GetNameFunc: func() string {
							return "test"
						},
						SupportsStrategyFunc: func(s string) bool {
							return s == "test"
						},
					},
				},
				configManager: &providers.ConfigManagerMock{
					GetStrategyMappingForDeploymentTypeFunc: func(ctx context.Context, t string) (*providers.DeploymentStrategyMapping, error) {
						return &providers.DeploymentStrategyMapping{
							SMTPCredentials: "test",
						}, nil
					},
				},
			},
			want: struct {
				Requeue      bool
				RequeueAfter time.Duration
			}{Requeue: true, RequeueAfter: 30 * time.Second},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ReconcileSMTPCredentialSet{
				client: tt.fields.client,
				scheme: tt.fields.scheme,
				logger: tt.fields.logger,
			}
			got, err := r.reconcile(tt.args.context, tt.args.request, tt.args.providerList, tt.args.configManager)
			if (err != nil) != tt.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Reconcile() got = %v, want %v", got, tt.want)
			}
		})
	}
}