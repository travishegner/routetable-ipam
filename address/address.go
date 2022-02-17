package address

import (
	"fmt"
	"net"
	"time"

	"github.com/TrilliumIT/iputil"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

const (
	//DefaultRequestedAddressSleepTime is the time to wait between attempts while searching for an address
	DefaultRequestedAddressSleepTime = 100 * time.Millisecond

	//DefaultRouteProtocol is the route protocol to use when inserting routes into the main routing table
	DefaultRouteProtocol = 192

	//DefaultPropagationTimeout is the minimum estimated time it takes routes to propagate across your cluster
	DefaultPropagationTimeout = 250 * time.Millisecond
)

//Address represents an address allocated by the IPAM
type Address struct {
	IPNet     *net.IPNet
	linkIndex int
}

//New allocates a new address from the network and claims it by installing it into the routing table
func New(cidr string, linkIndex, excludeFirst, excludeLast int) (*Address, error) {
	message := fmt.Sprintf("address.New(%v, %v, %v, %v)", cidr, linkIndex, excludeFirst, excludeLast)
	log.Debugf(message)
	handlerr := func(err error) error {
		log.WithError(err).Error(message)
		return fmt.Errorf("%v: %w", message, err)
	}

	a, err := Get(cidr, linkIndex)
	if err != nil {
		return nil, handlerr(err)
	}

	search := false
	var startAddr net.IP
	var firstAddr net.IP
	var lastAddr net.IP
	if a.IP().Equal(iputil.FirstAddr(a.IPNet)) {
		search = true
		a.IPNet.IP = iputil.RandAddrWithExclude(iputil.NetworkID(a.IPNet), excludeFirst, excludeLast)
		startAddr = a.IP()
		firstAddr = iputil.IPAdd(iputil.FirstAddr(a.IPNet), excludeFirst)
		lastAddr = iputil.IPAdd(iputil.LastAddr(a.IPNet), -1*excludeLast)
	}

	for {
		err = a.attempt()
		if err == nil {
			break
		}

		if search {
			a.IPNet.IP = iputil.IPAdd(a.IP(), 1)

			//roll over network end
			if a.IP().Equal(lastAddr) {
				a.IPNet.IP = iputil.IPAdd(firstAddr, 1)
			}

			if a.IP().Equal(startAddr) {
				return nil, handlerr(fmt.Errorf("exhausted address space and found no available address in %v", iputil.NetworkID(a.IPNet)))
			}
		}

		time.Sleep(DefaultRequestedAddressSleepTime)
	}

	return a, nil
}

//Get returns a populated Address struct
func Get(cidr string, linkIndex int) (*Address, error) {
	message := fmt.Sprintf("address.Get(%v, %v)", cidr, linkIndex)
	log.Debugf(message)
	handlerr := func(err error) error {
		return fmt.Errorf("%v: %w", message, err)
	}

	ipnet, err := netlink.ParseIPNet(cidr)
	if err != nil {
		return nil, handlerr(err)
	}

	return &Address{
		IPNet:     ipnet,
		linkIndex: linkIndex,
	}, nil
}

//IP Returns the IP of the Address
func (a *Address) IP() net.IP {
	return a.IPNet.IP
}

func (a *Address) attempt() error {
	message := "a.attempt()"
	log.Debugf(message)
	handlerr := func(err error) error {
		log.WithError(err).Error(message)
		return fmt.Errorf("%v: %w", message, err)
	}

	if a.IPNet == nil {
		return handlerr(fmt.Errorf("ipnet cannot be nil"))
	}
	if a.IP().Equal(iputil.FirstAddr(a.IPNet)) {
		return handlerr(fmt.Errorf("cannot request the network ID %v", a.IPNet))
	}
	if a.IP().Equal(iputil.LastAddr(a.IPNet)) {
		return handlerr(fmt.Errorf("cannot request the broadcast address %v", a.IPNet))
	}

	numRoutes, err := a.numRoutes()
	if err != nil {
		return handlerr(err)
	}
	if numRoutes > 0 {
		return handlerr(fmt.Errorf("address %v already in use", a.IPNet))
	}

	// add host route to routing table
	err = netlink.RouteAdd(&netlink.Route{
		LinkIndex: a.linkIndex,
		Dst:       a.hostNet(),
		Protocol:  DefaultRouteProtocol,
	})
	if err != nil {
		return handlerr(err)
	}

	//wait for at least estimated route propagation time
	time.Sleep(DefaultPropagationTimeout)

	//check that we are still the only route
	numRoutes, err = a.numRoutes()
	if err != nil {
		err2 := a.Delete()
		if err2 != nil {
			return handlerr(err2)
		}
		return handlerr(err)
	}

	if numRoutes < 1 {
		// The route either wasn't successfully added, or was removed,
		// let the outer loop try again
		return handlerr(fmt.Errorf("added %v to the routing table, but it was gone when we checked", a.IPNet))
	}

	if numRoutes == 1 {
		return nil
	}

	//address already in use
	err = a.Delete()
	if err != nil {
		return handlerr(err)
	}

	return handlerr(fmt.Errorf("selected %v, but someone else selected it at the same time", a.IPNet))
}

func (a *Address) hostNet() *net.IPNet {
	_, ml := a.IPNet.Mask.Size()
	return &net.IPNet{
		IP:   a.IP(),
		Mask: net.CIDRMask(ml, ml),
	}
}

//Delete deletes the address from the routing table
func (a *Address) Delete() error {
	message := "a.Delete()"
	log.Debugf(message)
	handlerr := func(err error) error {
		return fmt.Errorf("%v: %w", message, err)
	}
	err := netlink.RouteDel(&netlink.Route{
		LinkIndex: a.linkIndex,
		Dst:       a.hostNet(),
		Protocol:  DefaultRouteProtocol,
	})

	if err != nil {
		return handlerr(err)
	}

	return nil
}

func (a *Address) numRoutes() (int, error) {
	message := "a.numRoutes()"
	handlerr := func(err error) error {
		return fmt.Errorf("%v: %w", message, err)
	}

	routes, err := netlink.RouteListFiltered(0, &netlink.Route{Dst: a.hostNet()}, netlink.RT_FILTER_DST)
	if err != nil {
		return -1, handlerr(err)
	}
	if len(routes) != 1 {
		return len(routes), nil
	}
	if len(routes[0].MultiPath) != 0 {
		return len(routes[0].MultiPath), nil
	}

	return 1, nil
}
