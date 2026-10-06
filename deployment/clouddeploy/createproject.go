package clouddeploy

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/couchbaselabs/cbdinocluster/utils/capellav4"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// maxProjectNameLen is the limit the v4 api puts on a project name.
const maxProjectNameLen = 128

// SharedProjectName is the project that init offers to find or create when no
// project ID is set.
const SharedProjectName = "CBDC2_SHARED"

// CheckNewProjectName rejects a name that a new cbdinocluster project cannot use.
func CheckNewProjectName(name string) error {
	if name == "" {
		return errors.New("a capella project name is required")
	}

	if utf8.RuneCountInString(name) > maxProjectNameLen {
		return errors.Errorf("capella project name %q is longer than %d characters",
			name, maxProjectNameLen)
	}

	// cbdinocluster deletes the cbdc2_ projects of the old layout, so a shared
	// project must never look like one.
	if strings.HasPrefix(name, "cbdc2_") {
		return errors.Errorf("capella project name %q starts with cbdc2_, "+
			"which cbdinocluster keeps for the projects it deletes", name)
	}

	return nil
}

// CreateProject creates a project for cbdinocluster clusters and returns its
// ID. It refuses a name that a project already has.
func (p *Deployer) CreateProject(ctx context.Context, name, description string) (string, error) {
	return CreateProject(ctx, p.logger, p.v4, p.tenantID, name, description)
}

// CreateProject creates a project in the organization and returns its ID. It
// refuses a name that a project already has.
func CreateProject(
	ctx context.Context,
	logger *zap.Logger,
	client *capellav4.Client,
	orgID string,
	name, description string,
) (string, error) {
	if err := CheckNewProjectName(name); err != nil {
		return "", err
	}

	projects, err := client.ListProjects(ctx, orgID)
	if err != nil {
		return "", errors.Wrap(err, "failed to list projects")
	}

	var sameNameIDs []string
	for _, project := range projects {
		if project.Name == name {
			sameNameIDs = append(sameNameIDs, project.ID)
		}
	}
	if len(sameNameIDs) == 1 {
		return "", errors.Errorf("a capella project named %q already exists with ID %s, "+
			"use that ID or pick another name", name, sameNameIDs[0])
	}
	if len(sameNameIDs) > 1 {
		return "", errors.Errorf("%d capella projects named %q already exist with IDs %s, "+
			"use one of these IDs or pick another name",
			len(sameNameIDs), name, strings.Join(sameNameIDs, ", "))
	}

	logger.Info("creating a capella project", zap.String("project-name", name))

	resp, err := client.CreateProject(ctx, orgID, &capellav4.CreateProjectRequest{
		Name:        name,
		Description: description,
	})
	if err != nil {
		return "", errors.Wrapf(err, "failed to create the capella project %q", name)
	}
	if resp.ID == "" {
		return "", errors.Errorf("capella created the project %q but returned no ID", name)
	}

	return resp.ID, nil
}

// FindSharedProject returns the oldest project named SharedProjectName, nil
// when there is none, and how many projects carry that name. It lists the
// projects once and creates nothing.
func FindSharedProject(
	ctx context.Context,
	client *capellav4.Client,
	orgID string,
) (*capellav4.ProjectInfo, int, error) {
	projects, err := client.ListProjects(ctx, orgID)
	if err != nil {
		return nil, 0, errors.Wrap(err, "failed to list projects")
	}

	var matches []*capellav4.ProjectInfo
	for _, project := range projects {
		if project.Name == SharedProjectName {
			matches = append(matches, project)
		}
	}

	return oldestProject(matches), len(matches), nil
}

// oldestProject returns the project with the earliest creation time, nil when
// the list is empty. A project with a time that does not parse sorts last.
func oldestProject(projects []*capellav4.ProjectInfo) *capellav4.ProjectInfo {
	if len(projects) == 0 {
		return nil
	}

	type datedProject struct {
		project   *capellav4.ProjectInfo
		createdAt time.Time
		dated     bool
	}
	dated := make([]datedProject, len(projects))
	for i, project := range projects {
		createdAt, err := time.Parse(time.RFC3339, project.Audit.CreatedAt)
		dated[i] = datedProject{project: project, createdAt: createdAt, dated: err == nil}
	}

	sort.SliceStable(dated, func(i, j int) bool {
		if dated[i].dated != dated[j].dated {
			return dated[i].dated
		}
		return dated[i].createdAt.Before(dated[j].createdAt)
	})

	return dated[0].project
}
