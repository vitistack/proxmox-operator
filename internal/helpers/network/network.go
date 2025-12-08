/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package network provides network-related helper functions
package network

import (
	"net"
)

// IsPrivateIP checks if an IP address is in the private range
func IsPrivateIP(ip string) bool {
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		return false
	}

	// Check against private IPv4 ranges (RFC 1918)
	privateRanges := []struct {
		start net.IP
		end   net.IP
	}{
		{net.ParseIP("10.0.0.0"), net.ParseIP("10.255.255.255")},
		{net.ParseIP("172.16.0.0"), net.ParseIP("172.31.255.255")},
		{net.ParseIP("192.168.0.0"), net.ParseIP("192.168.255.255")},
	}

	for _, r := range privateRanges {
		if bytesCompare(parsedIP.To4(), r.start.To4()) >= 0 &&
			bytesCompare(parsedIP.To4(), r.end.To4()) <= 0 {
			return true
		}
	}

	return false
}

// bytesCompare compares two byte slices
func bytesCompare(a, b []byte) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// IPAddresses holds categorized IP addresses
type IPAddresses struct {
	PublicIPv4  string
	PrivateIPv4 string
	PublicIPv6  string
	PrivateIPv6 string
}

// CategorizeIPs categorizes a list of IP addresses
func CategorizeIPs(ips []string) IPAddresses {
	result := IPAddresses{}

	for _, ip := range ips {
		parsedIP := net.ParseIP(ip)
		if parsedIP == nil {
			continue
		}

		if parsedIP.To4() != nil {
			// IPv4
			if IsPrivateIP(ip) {
				if result.PrivateIPv4 == "" {
					result.PrivateIPv4 = ip
				}
			} else {
				if result.PublicIPv4 == "" {
					result.PublicIPv4 = ip
				}
			}
		} else {
			// IPv6
			if parsedIP.IsPrivate() {
				if result.PrivateIPv6 == "" {
					result.PrivateIPv6 = ip
				}
			} else {
				if result.PublicIPv6 == "" {
					result.PublicIPv6 = ip
				}
			}
		}
	}

	return result
}
