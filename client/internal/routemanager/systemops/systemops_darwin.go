//go:build darwin && !ios

package systemops

import (
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	log "github.com/sirupsen/logrus"

	"github.com/netbirdio/netbird/client/internal/peer"
)

func (r *RoutingManager) SetupRouting(initAddresses []net.IP) (peer.BeforeAddPeerHookFunc, peer.AfterRemovePeerHookFunc, error) {
	return r.setupRefCounter(initAddresses)
}

func (r *RoutingManager) CleanupRouting() error {
	return r.cleanupRefCounter()
}

func (r *RoutingManager) addToRouteTable(prefix netip.Prefix, nexthop Nexthop) error {
	return r.routeCmd("add", prefix, nexthop)
}

func (r *RoutingManager) removeFromRouteTable(prefix netip.Prefix, nexthop Nexthop) error {
	return r.routeCmd("delete", prefix, nexthop)
}

func (r *RoutingManager) routeCmd(action string, prefix netip.Prefix, nexthop Nexthop) error {
	inet := "-inet"
	network := prefix.String()
	if prefix.IsSingleIP() {
		network = prefix.Addr().String()
	}

	args := []string{"-n", action}
	if prefix.Addr().Is6() {
		inet = "-inet6"
		// TODO: Remove once we have IPv6 support on the interface
		// Point the route to lo0 if the nexthop is the WireGuard interface, otherwise the operation fails
		// without IPv6 support on the interface.
		if nexthop.Intf != nil && nexthop.Intf.Name == r.wgInterface.Name() {
			args = append(args, "-blackhole")
			nexthop.Intf = &net.Interface{Name: "lo0"}
		}
	}

	args = append(args, inet, network)
	if nexthop.IP.IsValid() {
		args = append(args, nexthop.IP.Unmap().String())
	} else if nexthop.Intf != nil {
		args = append(args, "-interface", nexthop.Intf.Name)
	}

	if err := retryRouteCmd(args); err != nil {
		return fmt.Errorf("failed to %s route for %s: %w", action, prefix, err)
	}
	return nil
}

func retryRouteCmd(args []string) error {
	operation := func() error {
		out, err := exec.Command("route", args...).CombinedOutput()
		log.Tracef("route %s: %s", strings.Join(args, " "), out)
		// https://github.com/golang/go/issues/45736
		if err != nil && strings.Contains(string(out), "sysctl: cannot allocate memory") {
			return err
		} else if err != nil {
			return backoff.Permanent(err)
		}
		return nil
	}

	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.InitialInterval = 50 * time.Millisecond
	expBackOff.MaxInterval = 500 * time.Millisecond
	expBackOff.MaxElapsedTime = 1 * time.Second

	err := backoff.Retry(operation, expBackOff)
	if err != nil {
		return fmt.Errorf("route cmd retry failed: %w", err)
	}
	return nil
}
