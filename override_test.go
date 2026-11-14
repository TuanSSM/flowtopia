package main

// The fetcher's dial override, declared here so that only a test can make one (overrider in main.go).

import (
	"context"
	"errors"
	"net"
)

// dialOverride lets a test reach its local stub: resolve stands in for DNS, and stub is the one address
// the guard lets through whatever its port and address.
type dialOverride struct {
	resolve func(host string) []string // host to "ip:port" addresses, in the order a resolver would give them
	stub    string                     // the stub's listening address
}

func (o *dialOverride) stubAddr() string { return o.stub }

// dial connects to the addresses resolve gives for the host, in turn, each through the guard, as
// net.Dialer does with the addresses of a DNS answer.
func (o *dialOverride) dial(ctx context.Context, d *net.Dialer, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs := o.resolve(host)
	if len(addrs) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	var errs []error
	for _, a := range addrs {
		c, err := d.DialContext(ctx, network, a)
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}
