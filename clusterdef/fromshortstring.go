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
	}

	return nil, fmt.Errorf("unknown short string name `%s`", defName)
}
