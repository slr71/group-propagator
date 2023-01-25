package main

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"

	"github.com/cyverse-de/go-mod/restutils"
	"github.com/cyverse-de/group-propagator/client/datainfo"
	"github.com/cyverse-de/group-propagator/client/groups"
)

// To propagate a group:
// * Fetch group details and members via iplant-groups
//   -> get a model.GrouperGroup and model.GrouperGroupMembers, probably
// * Determine iRODS group name (@grouper-<GrouperGroup.ID>)
// * Create or update group with proper membership list via data-info, potentially validating users/etc.

type Propagator struct {
	groupsClient *groups.GroupsClient
	groupPrefix  string

	dataInfoClient *datainfo.DataInfoClient
}

func NewPropagator(groupsClient *groups.GroupsClient, groupPrefix string, dataInfoClient *datainfo.DataInfoClient) *Propagator {
	if groupPrefix == "" {
		groupPrefix = "@grouper-"
	}

	return &Propagator{
		groupsClient:   groupsClient,
		groupPrefix:    groupPrefix,
		dataInfoClient: dataInfoClient,
	}
}

func (p *Propagator) PropagateGroupById(ctx context.Context, groupID string) error {
	ctx, span := otel.Tracer(otelName).Start(ctx, "PropagateGroupByID")
	defer span.End()

	irodsName := fmt.Sprintf("%s%s", p.groupPrefix, groupID)

	g, err := p.groupsClient.GetGroupByID(ctx, groupID)
	if restutils.GetStatusCode(err) == 404 {
		err = p.dataInfoClient.DeleteGroup(ctx, irodsName)
		if err != nil {
			err = errors.Wrap(err, "Error deleting group")
		}
		return err
	} else if err != nil {
		return errors.Wrap(err, "Failed fetching Grouper group by ID")
	}

	irodsName = fmt.Sprintf("%s%s", p.groupPrefix, g.ID)

	members, err := p.groupsClient.GetGroupMembers(ctx, g.Name)
	if err != nil {
		return errors.Wrap(err, "Failed fetching Grouper group members")
	}

	var irodsMembers []string
	for _, member := range members.Members {
		if member.SourceID == "ldap" {
			irodsMembers = append(irodsMembers, member.ID)
		} else {
			log.Errorf("Could not add group member %+v", member)
		}
	}

	irodsGroupExists := true

	// Check if group exists/has members, but we don't need to care what members
	_, err = p.dataInfoClient.ListGroupMembers(ctx, irodsName)
	if restutils.GetStatusCode(err) == 404 {
		irodsGroupExists = false
	} else if err != nil {
		return errors.Wrap(err, "Failed fetching existing iRODS group members")
	}

	var finalGroup datainfo.Group

	if irodsGroupExists {
		finalGroup, err = p.dataInfoClient.UpdateGroupMembers(ctx, irodsName, irodsMembers)
	} else {
		finalGroup, err = p.dataInfoClient.CreateGroup(ctx, irodsName, irodsMembers)
	}

	if err != nil {
		return errors.Wrapf(err, "Failed creating or updating group %s (%s) -> %s with %d members", g.Name, groupID, finalGroup.Name, len(irodsMembers))
	}

	log.Infof("Updated group %s (%s) -> %s with %d members", g.Name, groupID, finalGroup.Name, len(finalGroup.Members))

	return nil
}
