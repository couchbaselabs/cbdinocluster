package cmd

import (
	"context"
	"fmt"

	"github.com/couchbaselabs/cbdinocluster/deployment/clouddeploy"
	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"go.uber.org/zap"
)

const capellaSharedProjectDescription = "Shared project for cbdinocluster clusters"

// chooseCapellaSharedProject helps a person with no project ID find or create
// the shared project. When that ends without a project, it asks for an ID. It
// returns the ID to save, or empty. A failed call prints the error, so init
// goes on.
func chooseCapellaSharedProject(
	ctx context.Context,
	logger *zap.Logger,
	client *capellav4.Client,
	orgID string,
	readBool func(q string, defaultValue bool) bool,
	readString func(q string, defaultValue string, sensitive bool) string,
) string {
	projectName := clouddeploy.SharedProjectName

	fmt.Printf("Looking up the %s project in Capella...\n", projectName)
	project, count, err := clouddeploy.FindSharedProject(ctx, client, orgID)
	if err != nil {
		fmt.Printf("Failed to look up the %s project.\n  %s\n", projectName, err)
		return promptCapellaProjectID(readString)
	}

	if project == nil {
		if !readBool(fmt.Sprintf("Could not find a %s project. Create one and use it?", projectName), false) {
			return promptCapellaProjectID(readString)
		}

		projectID, err := clouddeploy.CreateProject(ctx, logger, client, orgID,
			projectName, capellaSharedProjectDescription)
		if err != nil {
			fmt.Printf("Failed to create the %s project.\n  %s\n", projectName, err)
			return promptCapellaProjectID(readString)
		}

		fmt.Printf("Created the shared Capella project %s (%s).\n", projectName, projectID)
		return checkedCapellaProjectID(projectID)
	}

	fmt.Printf("Found the shared Capella project %s (%s).\n", projectName, project.ID)
	if count > 1 {
		fmt.Printf("%d projects carry this name, using the oldest.\n", count)
	}

	if readBool("Use it for new clusters?", true) {
		return checkedCapellaProjectID(project.ID)
	}

	return promptCapellaProjectID(readString)
}

// promptCapellaProjectID asks until it gets a valid ID or an empty answer.
func promptCapellaProjectID(
	readString func(q string, defaultValue string, sensitive bool) string,
) string {
	for {
		projectID := readString("What Capella project ID should clusters go into?", "", false)
		if projectID == "" {
			return ""
		}
		if err := clouddeploy.CheckProjectID(projectID); err != nil {
			fmt.Printf("%s\n", err)
			continue
		}
		return projectID
	}
}

// checkedCapellaProjectID drops an ID that the deployer would refuse, so a
// bad answer from Capella cannot break every later command.
func checkedCapellaProjectID(projectID string) string {
	if err := clouddeploy.CheckProjectID(projectID); err != nil {
		fmt.Printf("%s\n", err)
		return ""
	}
	return projectID
}
