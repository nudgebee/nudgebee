package license

import (
	"crypto/rsa"
	_ "encoding/base64"
	"errors"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"net/http"
	"nudgebee/services/config"
	"time"

	"github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
)

var publicKey = `-----BEGIN PUBLIC KEY-----
MIICIjANBgkqhkiG9w0BAQEFAAOCAg8AMIICCgKCAgEAxUA6G0H099Sac65x4ipd
NuEunAfhH7Gk6O36wNlRw4UBAZcBNljUVZ34oDUYRDurvjkiFXd/Zim8J6j1t2qc
yJ6ONWOWD6BPgJ5Edw+o2ReJnX5JXDuavseg5zbr6+UQVQ5s31xerj/SNCKxyvvg
cBsZRmQT7jFlc9V2yIfTyQAtCWh+bIwsBRM+rG6ivL83hy54gMDWxPZwnhvkY1/V
WpcklGQYwvrXsbotrkTZSDWNSv5gW6tnz1dqiXZVaJmzWiVP3CpB+Yk2OxWJ4HlS
3Sru+fqqNBrwPapZOTSbtBBcYszogy0KaeHjl8Px27YdHhJWqUI/wkJqUDwHFjZw
i9gqCZcTM2niPbqXL9g82Zn7TqxK88H6mN4jLargyF9A81e1Ey7VzHNwZ+qY7cj1
AUkCmJd+PaIrb/kNzm9CqsXc1yRFM4T8U4G6OmFHQe+M+roZOysmTyqgon5LOx0z
kzFZn969IRsNvY1VMibxf8VUcXsCtxnJDGnSYM/fANxPIqlS/R3fofJvsP1qfgSB
oShfUiDgPKAhRPnmotKgERrc0kOz+yie+rtlL04KBE1vTIgPWIWUm5XaZ5KSUphe
ZKyS6YnDj5zquCji1jLlZLFc68KsRMQBVadrR07P7oXgsL3hm3iuneN1spwDdUIU
Oqf3hxqlZqi93uE2J18VNB0CAwEAAQ==
-----END PUBLIC KEY-----`

type Claims struct {
	TenantId           string `json:"tenantId"`
	LicenseType        string `json:"licenseType"`
	Email              string `json:"email"`
	Iat                int64  `json:"iat"`
	Sub                string `json:"sub"`
	Exp                int64  `json:"exp"`
	MaxAccounts        int    `json:"max_clusters"`
	MaxNodesPerCluster int    `json:"max_nodes_per_cluster"`
	jwt.RegisteredClaims
}

func parsePublicKey(pemKey string) (*rsa.PublicKey, error) {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(pemKey))
	if err != nil {
		return nil, err
	}
	return pubKey, nil
}

func CheckLimits(c *gin.Context, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {
	licenseKey := config.Config.NudgebeeLicense
	if licenseKey == "" {
		logger.Warn("License not found")
		c.JSON(http.StatusOK, gin.H{
			"license": gin.H{
				"max_accounts":          -1,
				"max_nodes_per_cluster": -1,
				"message":               "License not found",
				"status":                "not_found",
			},
		})
		return
	}

	pubKey, err := parsePublicKey(publicKey)
	if err != nil {
		logger.Error("Unable to parse public key", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"license": gin.H{
				"max_accounts":          -1,
				"max_nodes_per_cluster": -1,
				"message":               "Unable to parse key for license",
				"status":                "error",
			},
		})
		return
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(licenseKey, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return pubKey, nil
	})

	if err != nil || !token.Valid {
		logger.Error("Unable to verify license:", "error", err)
		c.JSON(http.StatusOK, gin.H{
			"license": gin.H{
				"max_accounts":          -1,
				"max_nodes_per_cluster": -1,
				"message":               "Unable to verify license",
				"status":                "error",
			},
		})
		return
	}

	maxAccounts := claims.MaxAccounts
	if maxAccounts == 0 {
		maxAccounts = -1
	}

	maxNodesPerCluster := claims.MaxNodesPerCluster
	if maxNodesPerCluster == 0 {
		maxNodesPerCluster = -1
	}

	if time.Now().Unix() > claims.Exp {
		logger.Warn("License expired")
		c.JSON(http.StatusOK, gin.H{
			"license": gin.H{
				"type":                  claims.LicenseType,
				"max_accounts":          maxAccounts,
				"max_nodes_per_cluster": maxNodesPerCluster,
				"message":               "Your license is expired on " + time.Unix(claims.Exp, 0).Format("2006-01-02"),
				"status":                "expired",
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"license": gin.H{
			"type":                  claims.LicenseType,
			"max_accounts":          maxAccounts,
			"max_nodes_per_cluster": maxNodesPerCluster,
			"message":               "Your license is valid till " + time.Unix(claims.Exp, 0).Format("2006-01-02"),
			"status":                "valid",
		},
	})
}

func GetMaxAccounts() int {
	licenseKey := config.Config.NudgebeeLicense

	if licenseKey == "" {
		return -1
	}

	pubKey, err := parsePublicKey(publicKey)
	if err != nil {
		return -1
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(licenseKey, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return pubKey, nil
	})

	if err != nil || !token.Valid {
		return -1
	}

	if time.Now().Unix() > claims.Exp {
		return 0
	}

	return claims.MaxAccounts
}
