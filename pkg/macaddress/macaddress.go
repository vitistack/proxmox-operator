package macaddress

type MacAddressGenerator interface {
	// GetMACAddress returns the MAC address of the machine
	GetMACAddress() (string, error)
}
