package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

type CloudProjectsCreateOutput struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var cloudProjectsCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Creates a Capella project for cbdinocluster clusters and prints its ID",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		helper := CmdHelper{}
		logger := helper.GetLogger()
		ctx := helper.GetContext()

		outputJson, _ := cmd.Flags().GetBool("json")
		description, _ := cmd.Flags().GetString("description")
		name := args[0]

		deployer := helper.GetCloudDeployer(ctx)
		if deployer == nil {
			logger.Fatal(`capella is not enabled, run "cbdinocluster init" first`)
		}

		projectID, err := deployer.CreateProject(ctx, name, description)
		if err != nil {
			logger.Fatal("failed to create the capella project", zap.Error(err))
		}

		// Only the ID goes to stdout, so a script can capture it.
		if !outputJson {
			fmt.Printf("%s\n", projectID)
		} else {
			helper.OutputJson(CloudProjectsCreateOutput{
				ID:   projectID,
				Name: name,
			})
		}

		fmt.Fprintf(os.Stderr, "Set it as the project for new clusters with \"cbdinocluster init --capella-project-id %s\".\n",
			projectID)
	},
}

func init() {
	cloudProjectsCmd.AddCommand(cloudProjectsCreateCmd)

	cloudProjectsCreateCmd.Flags().String("description", "", "Description of the new project")
}
