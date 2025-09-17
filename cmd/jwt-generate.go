package cmd

import (
	"fmt"
	"time"

	"github.com/couchbaselabs/cbdinocluster/utils/dinocerts"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	jwt "github.com/golang-jwt/jwt/v5"
)

type customJwtClaims struct {
	Roles []string `json:"roles"`
	jwt.RegisteredClaims
}

var jwtGenerateCmd = &cobra.Command{
	Use:   "generate <username>",
	Short: "Fetches a JWT token for a specific set of roles",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()

		username := args[0]
		canRead, _ := cmd.Flags().GetBool("can-read")
		canWrite, _ := cmd.Flags().GetBool("can-write")
		expiresInStr, _ := cmd.Flags().GetString("expires-in")

		var expiresInClaim *jwt.NumericDate
		if expiresInStr == "none" {
			// leave the expiresInClaim as nil
		} else {
			expiresIn, err := time.ParseDuration(expiresInStr)
			if err != nil {
				logger.Fatal("failed to parse expires-in duration", zap.Error(err))
			}

			expiresInClaim = jwt.NewNumericDate(time.Now().Add(expiresIn))
		}

		var roles []string
		if canWrite {
			roles = append(roles, "admin")
		} else if canRead {
			roles = append(roles,
				"ro_admin",
				"analytics_reader",
				"data_reader[*]",
				"views_reader[*]",
				"query_select[*]",
				"fts_searcher[*]")
		}

		rootCa, err := dinocerts.GetRootCertAuthority()
		if err != nil {
			logger.Fatal("failed to get dino certificate authority", zap.Error(err))
		}

		_, privKey, err := rootCa.GetRS256SigningKeys()
		if err != nil {
			logger.Fatal("failed to get jwt signing keys", zap.Error(err))
		}

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, &customJwtClaims{
			Roles: roles,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "dino",
				Subject:   username,
				Audience:  []string{"client"},
				ExpiresAt: expiresInClaim,
			},
		})

		signedToken, err := token.SignedString(privKey)
		if err != nil {
			logger.Fatal("failed to sign token", zap.Error(err))
		}

		fmt.Printf("%s\n", signedToken)
	},
}

func init() {
	jwtCmd.AddCommand(jwtGenerateCmd)
	jwtGenerateCmd.Flags().Bool("can-read", true, "Whether the token can read data")
	jwtGenerateCmd.Flags().Bool("can-write", true, "Whether the token can write data")
	jwtGenerateCmd.Flags().String("expires-in", "8766h", "How long before the token expires (e.g. 24h, 30m, 10s, -1h) or 'none' for no expiration claim")
}
