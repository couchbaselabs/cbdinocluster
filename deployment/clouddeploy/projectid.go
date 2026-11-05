package clouddeploy

import (
	"regexp"

	"github.com/pkg/errors"
)

var projectIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// CheckProjectID rejects a value that is not a Capella project ID. This
// catches a project name pasted in place of the ID.
func CheckProjectID(projectID string) error {
	if !projectIDPattern.MatchString(projectID) {
		return errors.Errorf("capella project id %q is not a UUID, use the project ID and not its name",
			projectID)
	}
	return nil
}
