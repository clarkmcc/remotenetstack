package utils

import (
	"fmt"
	"net"
	"net/netip"
)

func ToNetIpAddr(ip net.IP) (netip.Addr, error) {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, fmt.Errorf("invalid net.IP: %v", ip)
	}
	return addr, nil
}

func ToNetIpPrefix(ipNet net.IPNet) (netip.Prefix, error) {
	addr, err := ToNetIpAddr(ipNet.IP)
	if err != nil {
		return netip.Prefix{}, err
	}
	ones, bits := ipNet.Mask.Size()
	if ones == 0 && bits == 0 {
		return netip.Prefix{}, fmt.Errorf("invalid net.IP: %v", ipNet)
	}
	return netip.PrefixFrom(addr, ones), nil
}
