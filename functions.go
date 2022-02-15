package main

import (
	"fmt"
	"net"
	"time"

	"github.com/TrilliumIT/iputil"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// SelectAddress returns an available IP or the requested IP (if available) or an error on timeout
func SelectAddress(addr *net.IPNet, xf, xl int) (*net.IPNet, error) {
	var err error
	sleepTime := time.Duration(DefaultRequestedAddressSleepTime) * time.Millisecond

	linkIndex, err := LinkIndexFromIPNet(addr)
	if err != nil {
		return nil, err
	}

	search := false
	randAddr := addr
	var startAddr net.IP
	var firstAddr net.IP
	var lastAddr net.IP
	if addr.IP.Equal(iputil.FirstAddr(addr)) {
		search = true
		randAddr.IP = iputil.RandAddrWithExclude(iputil.NetworkID(addr), xf, xl)
		startAddr = randAddr.IP
		firstAddr = iputil.IPAdd(iputil.FirstAddr(addr), xf)
		lastAddr = iputil.IPAdd(iputil.LastAddr(addr), -1*xl)
	}

	for {
		log.Debugf("attempting to provision %v", randAddr)
		err = attemptAddress(randAddr, linkIndex)
		if err == nil {
			break
		}

		log.WithError(err).Warningf("unable to provision %v", randAddr)

		if search {
			randAddr.IP = iputil.IPAdd(randAddr.IP, 1)

			if randAddr.IP.Equal(lastAddr) {
				randAddr.IP = iputil.IPAdd(firstAddr, 1)
			}

			if randAddr.IP.Equal(startAddr) {
				return nil, fmt.Errorf("exhausted address space and found no available address in %v", addr)
			}
		}

		time.Sleep(sleepTime)
	}

	return randAddr, nil
}

// attemptAddress attempts to provision the requested address and returns an error if it is not able
func attemptAddress(reqAddress *net.IPNet, linkIndex int) error {
	if reqAddress == nil {
		return fmt.Errorf("reqAddress cannot be nil")
	}
	if reqAddress.IP.Equal(iputil.FirstAddr(reqAddress)) {
		return fmt.Errorf("cannot request the network ID of a subnet")
	}
	if reqAddress.IP.Equal(iputil.LastAddr(reqAddress)) {
		return fmt.Errorf("cannot request the broadcast address of a subnet")
	}

	_, addrOnly := GetIPNets(reqAddress.IP, reqAddress)

	numRoutes, err := numRoutesTo(addrOnly)
	if err != nil {
		return err
	}
	if numRoutes > 0 {
		return fmt.Errorf("address %v already in use", reqAddress)
	}

	// add host route to routing table
	err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: linkIndex,
		Dst:       addrOnly,
		Protocol:  DefaultRouteProtocol,
	})
	if err != nil {
		return err
	}

	//wait for at least estimated route propagation time
	time.Sleep(time.Duration(DefaultPropagationTimeout) * time.Millisecond)

	//check that we are still the only route
	numRoutes, err = numRoutesTo(addrOnly)
	if err != nil {
		err2 := DelRoute(linkIndex, addrOnly)
		if err2 != nil {
			return err2
		}
		return err
	}

	if numRoutes < 1 {
		// The route either wasn't successfully added, or was removed,
		// let the outer loop try again
		return fmt.Errorf("added %v to the routing table, but it was gone when we checked", addrOnly)
	}

	if numRoutes == 1 {
		return nil
	}

	//address already in use
	err = DelRoute(linkIndex, addrOnly)
	if err != nil {
		return err
	}

	return fmt.Errorf("selected %v, but someone else selected it at the same time", addrOnly)
}

//GetIPNets takes an IP and a subnet and returns the IPNet representing the IP in the subnet,
//as well as an IPNet representing the "host only" cidr
//in other words a /32 in IPv4 or a /128 in IPv6
func GetIPNets(address net.IP, subnet *net.IPNet) (*net.IPNet, *net.IPNet) {
	sna := &net.IPNet{
		IP:   address,
		Mask: address.DefaultMask(),
	}

	//address in big subnet
	if subnet != nil {
		sna.Mask = subnet.Mask
	}

	if sna.Mask == nil {
		sna.Mask = net.CIDRMask(128, 128)
	}

	_, ml := sna.Mask.Size()
	a := &net.IPNet{
		IP:   address,
		Mask: net.CIDRMask(ml, ml),
	}

	return sna, a
}

func numRoutesTo(ipnet *net.IPNet) (int, error) {
	routes, err := netlink.RouteListFiltered(0, &netlink.Route{Dst: ipnet}, netlink.RT_FILTER_DST)
	if err != nil {
		return -1, err
	}
	if len(routes) != 1 {
		return len(routes), nil
	}
	return len(routes[0].MultiPath), nil
}

// DelRoute deletes the /32 or /128 to the passed address
func DelRoute(linkIndex int, ip *net.IPNet) error {
	return netlink.RouteDel(&netlink.Route{
		LinkIndex: linkIndex,
		Dst:       ip,
		Protocol:  DefaultRouteProtocol,
	})
}

//LinkIndexFromIPNet gets the link index of the first interface which is on the same subnet as the parameter
func LinkIndexFromIPNet(address *net.IPNet) (int, error) {
	routes, err := netlink.RouteGet(address.IP)
	if err != nil {
		return -1, err
	}

	for _, r := range routes {
		if r.Gw != nil {
			continue
		}

		return r.LinkIndex, nil
	}

	return -1, fmt.Errorf("interface not found")
}
