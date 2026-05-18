package transform

import (
	"context"
	"fmt"
	"net"
)

func validateRemoteHost(host string) error {
	if host == "localhost" || host == "" {
		return fmt.Errorf("host %q is not allowed (private/loopback)", host)
	}
	ips, err := net.LookupHost(host)
	if err != nil {
		return nil // DNS may not be available at init; dial-time check is the real guard
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip != nil && isPrivateIP(ip) {
			return fmt.Errorf("host %q resolves to private address %s", host, ipStr)
		}
	}
	return nil
}

func ssrfSafeDialContext() func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ipStr := range ips {
			ip := net.ParseIP(ipStr)
			if ip != nil && isPrivateIP(ip) {
				return nil, fmt.Errorf("connection to private address %s refused", ipStr)
			}
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
	}
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
