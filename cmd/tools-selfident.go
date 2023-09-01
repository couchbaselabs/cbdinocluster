package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/utils/awscontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/azurecontrol"
	"github.com/couchbaselabs/cbdinocluster/utils/cloudinstancecontrol"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var toolsSelfIdentCmd = &cobra.Command{
	Use:   "self-ident",
	Short: "Automatically determines the current cloud instance.",
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		siCtrl := cloudinstancecontrol.SelfIdentifyController{
			Logger: logger,
		}

		selfIdentity, err := siCtrl.Identify(ctx)
		if err != nil {
			logger.Fatal("failed fetch self identity", zap.Error(err))
		}

		var jsonBytes []byte
		switch selfIdentity := selfIdentity.(type) {
		case *awscontrol.LocalInstanceInfo:
			awsJsonBytes, err := json.Marshal(struct {
				Type       string `json:"type"`
				Region     string `json:"region"`
				InstanceID string `json:"instance_id"`
			}{
				Type:       "aws",
				Region:     selfIdentity.Region,
				InstanceID: selfIdentity.InstanceID,
			})
			if err != nil {
				logger.Fatal("failed to marshal aws json", zap.Error(err))
			}

			jsonBytes = awsJsonBytes
		case *azurecontrol.LocalVmInfo:
			azureJsonBytes, err := json.Marshal(struct {
				Type   string `json:"type"`
				Region string `json:"region"`
				VmID   string `json:"vm_id"`
			}{
				Type:   "azure",
				Region: selfIdentity.Region,
				VmID:   selfIdentity.VmID,
			})
			if err != nil {
				logger.Fatal("failed to marshal aws json", zap.Error(err))
			}

			jsonBytes = azureJsonBytes
		default:
			jsonBytes = []byte(`{"type":"unknown"}`)
		}

		fmt.Printf("%s\n", jsonBytes)
	},
}

func init() {
	toolsCmd.AddCommand(toolsSelfIdentCmd)
}
