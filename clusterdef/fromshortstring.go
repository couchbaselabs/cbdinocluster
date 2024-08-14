package clusterdef

import (
	"errors"
	"fmt"
	"strings"
)

func FromShortString(shortStr string) (*Cluster, error) {
	defIdParts := strings.Split(shortStr, ":")
	if len(defIdParts) != 2 {
		return nil, errors.New("unexpected short string format")
	}

	defName := defIdParts[0]
	defVersion := defIdParts[1]

	if defName == "simple" {
		return &Cluster{
			NodeGroups: []*NodeGroup{
				{
					Count:   3,
					Version: defVersion,
					Services: []Service{
						KvService,
						QueryService,
						IndexService,
						SearchService,
					},
				},
			},
		}, nil
	} else if defName == "single" {
		return &Cluster{
			NodeGroups: []*NodeGroup{
				{
					Count:   1,
					Version: defVersion,
					Services: []Service{
						KvService,
						QueryService,
						IndexService,
						SearchService,
					},
				},
			},
		}, nil
	} else if defName == "high-mem" {
		return &Cluster{
			NodeGroups: []*NodeGroup{
				{
					Count:   1,
					Version: defVersion,
					Services: []Service{
						KvService,
						QueryService,
						IndexService,
						SearchService,
					},
				},
			},
			Docker: DockerCluster{
				KvMemoryMB:    1536,
				IndexMemoryMB: 1024,
				FtsMemoryMB:   1024,
			},
		}, nil
	} else if defName == "columnar" {
		return &Cluster{
			Columnar: true,
			NodeGroups: []*NodeGroup{
				{
					Count:   3,
					Version: defVersion,
				},
			},
		}, nil
	} else if defName == "columnar-single" {
		return &Cluster{
			Columnar: true,
			NodeGroups: []*NodeGroup{
				{
					Count:   1,
					Version: defVersion,
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("unknown short string name `%s`", defName)
}
