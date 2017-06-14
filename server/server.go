/*

Copyright 2017 Google Inc.

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

package server

import (
	"context"
	"net"
	"time"

	"github.com/golang/glog"
	vmregistrypb "github.com/google/vmregistry/api"
	dhcp "github.com/krolaw/dhcp4"
	opentracing "github.com/opentracing/opentracing-go"
)

const leaseDuration = 24 * time.Hour

// Server implements a DHCP server.
type Server struct {
	vmregClient vmregistrypb.VMRegistryClient
	serverIP    net.IP
	options     dhcp.Options
}

// NewServer creates a new server.
func NewServer(
	vmregClient vmregistrypb.VMRegistryClient,
	serverIP net.IP,
	subnet net.IPMask,
	gateway net.IP,
	dns net.IP,
	domainName string) Server {
	options := dhcp.Options{
		dhcp.OptionSubnetMask:       subnet,
		dhcp.OptionRouter:           gateway,
		dhcp.OptionDomainNameServer: dns,
	}
	if domainName != "" {
		options[dhcp.OptionDomainName] = []byte(domainName)
	}
	glog.Infof("serving as id %s, subnet %s gw %s dns %s domain %s", serverIP, subnet, gateway, dns, domainName)
	return Server{
		vmregClient: vmregClient,
		serverIP:    serverIP,
		options:     options,
	}
}

func (s Server) findVM(ctx context.Context, sp opentracing.Span, req dhcp.Packet) (*vmregistrypb.VM, net.IP) {
	macString := req.CHAddr().String()
	vm, err := s.vmregClient.Find(ctx, &vmregistrypb.FindRequest{FindBy: vmregistrypb.FindRequest_MAC, Value: macString})
	if err != nil {
		sp.SetTag("error", true)
		glog.Errorf("failed to get ip for mac %s: %v", macString, err)
		return nil, nil
	}
	ip := net.ParseIP(vm.Ip)
	if ip == nil {
		sp.SetTag("error", true)
		glog.Errorf("failed to parse ip %s", vm.Ip)
		return nil, nil
	}
	return vm, ip
}

func (s Server) dhcpDiscover(ctx context.Context, req dhcp.Packet, options dhcp.Options) dhcp.Packet {
	sp, ctx := opentracing.StartSpanFromContext(ctx, "dhcp.discover")
	defer sp.Finish()

	vm, ip := s.findVM(ctx, sp, req)
	if vm == nil {
		return nil
	}

	glog.Infof("offering %s to %s [%s]", vm.Ip, vm.Name, req.CHAddr().String())
	return dhcp.ReplyPacket(req, dhcp.Offer, s.serverIP, ip, leaseDuration,
		s.options.SelectOrderOrAll(options[dhcp.OptionParameterRequestList]))
}

func (s Server) dhcpRequest(ctx context.Context, req dhcp.Packet, options dhcp.Options) dhcp.Packet {
	sp, ctx := opentracing.StartSpanFromContext(ctx, "dhcp.request")
	defer sp.Finish()

	if server, ok := options[dhcp.OptionServerIdentifier]; ok && !net.IP(server).Equal(s.serverIP) {
		glog.Infof("message for a different server?")
		return nil
	}
	reqIP := net.IP(options[dhcp.OptionRequestedIPAddress])
	if reqIP == nil {
		reqIP = net.IP(req.CIAddr())
	}

	if len(reqIP) == 4 && !reqIP.Equal(net.IPv4zero) {
		vm, _ := s.findVM(ctx, sp, req)
		if vm == nil {
			glog.Warningf("nak to %s: VM not found", req.CHAddr().String())
			return dhcp.ReplyPacket(req, dhcp.NAK, s.serverIP, nil, 0, nil)
		}
		if vm.Ip != reqIP.String() {
			glog.Warningf("nak to %s: expected %s, requested %s", req.CHAddr().String(), vm.Ip, reqIP.String())
			return dhcp.ReplyPacket(req, dhcp.NAK, s.serverIP, nil, 0, nil)
		}

		return dhcp.ReplyPacket(req, dhcp.ACK, s.serverIP, reqIP, leaseDuration,
			s.options.SelectOrderOrAll(options[dhcp.OptionParameterRequestList]))
	}
	return dhcp.ReplyPacket(req, dhcp.NAK, s.serverIP, nil, 0, nil)
}

// ServeDHCP handles incoming dhcp requests.
func (s Server) ServeDHCP(req dhcp.Packet, msgType dhcp.MessageType, options dhcp.Options) dhcp.Packet {
	sp, ctx := opentracing.StartSpanFromContext(context.Background(), "dhcp")
	defer sp.Finish()

	switch msgType {
	case dhcp.Discover:
		return s.dhcpDiscover(ctx, req, options)
	case dhcp.Request:
		return s.dhcpRequest(ctx, req, options)
	}

	return nil
}
