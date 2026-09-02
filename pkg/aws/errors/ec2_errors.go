package errors

const (
	// DuplicateVlanID is returned by AssociateTrunkInterface when the requested
	// VLAN is already associated on the trunk. Observed signature:
	// "InvalidVlanId.Duplicate: VlanId '1' is in use".
	DuplicateVlanID       = "InvalidVlanId.Duplicate"
	NotFoundAssociationID = "InvalidAssociationID.NotFound"
	NotFoundInterfaceID   = "InvalidNetworkInterfaceID.NotFound"
)
