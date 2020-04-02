package resources

import (
	"os"
	"strconv"
	"time"

	errorUtil "github.com/pkg/errors"
	"github.com/sethvargo/go-password/password"
)

const (
	EnvForceReconcileTimeout = "ENV_FORCE_RECONCILE_TIMEOUT"
	DefaultTagKeyPrefix      = "integreatly.org/"
	ErrorReconcileTime       = time.Second * 30
	SuccessReconcileTime     = time.Second * 60
)

//GetForcedReconcileTimeOrDefault returns envar for reconcile time else returns default time
func GetForcedReconcileTimeOrDefault(defaultTo time.Duration) time.Duration {
	recTime, exist := os.LookupEnv(EnvForceReconcileTimeout)
	if exist {
		rt, err := strconv.ParseInt(recTime, 10, 64)
		if err != nil {
			return defaultTo
		}
		return time.Duration(rt)
	}
	return defaultTo
}

func GeneratePassword() (string, error) {
	generatedPassword, err := password.Generate(32, 10, 0, false, false)
	if err != nil {
		return "", errorUtil.Wrap(err, "error generating password")
	}
	return generatedPassword, nil
}

func GetOrganizationTag() string {
	// get the environment from the CR
	organizationTag, exists := os.LookupEnv("TAG_KEY_PREFIX")
	if !exists {
		organizationTag = DefaultTagKeyPrefix
	}
	return organizationTag
}
