package errors

const (
	// DuplicateVlanID means the trunk already uses the requested VLAN.
	DuplicateVlanID       = "InvalidVlanId.Duplicate"
	NotFoundAssociationID = "InvalidAssociationID.NotFound"
	NotFoundInterfaceID   = "InvalidNetworkInterfaceID.NotFound"
)
