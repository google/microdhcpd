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

package main

import (
	"flag"
	"fmt"
	"net"

	"github.com/golang/glog"
	"github.com/google/credstore/client"
	"github.com/google/go-microservice-helpers/client"
	"github.com/google/go-microservice-helpers/tracing"
	vmregistrypb "github.com/google/vmregistry/api"
	"github.com/krolaw/dhcp4"
	"golang.org/x/net/context"
	"google.golang.org/grpc"

	"github.com/google/microdhcpd/server"
)

var (
	listenIf = flag.String("interface", "", "interface to listen on")

	credStoreAddress = flag.String("credstore-address", "", "credstore grpc address")
	credStoreCA      = flag.String("credstore-ca", "", "credstore server ca")

	vmregistryAddress = flag.String("vmregistry-address", "127.0.0.1:9000", "vm registry grpc address")
	vmregistryCA      = flag.String("vmregistry-ca", "", "vm registry server ca")

	serverIP   = flag.String("server-ip", "", "this server ip for identification")
	subnet     = flag.String("subnet", "", "announced subnet")
	gateway    = flag.String("gateway", "", "announced gateway")
	dns        = flag.String("dns", "", "announced dns")
	domainName = flag.String("domain-name", "", "announced dns domain name")
)

type vmregistryBearerClient struct {
	cli vmregistrypb.VMRegistryClient
	tok string
}

func (c vmregistryBearerClient) List(ctx context.Context, in *vmregistrypb.ListVMRequest, opts ...grpc.CallOption) (*vmregistrypb.ListVMReply, error) {
	return c.cli.List(client.WithBearerToken(ctx, c.tok), in, opts...)
}
func (c vmregistryBearerClient) Find(ctx context.Context, in *vmregistrypb.FindRequest, opts ...grpc.CallOption) (*vmregistrypb.VM, error) {
	return c.cli.Find(client.WithBearerToken(ctx, c.tok), in, opts...)
}
func (c vmregistryBearerClient) Create(ctx context.Context, in *vmregistrypb.CreateRequest, opts ...grpc.CallOption) (*vmregistrypb.VM, error) {
	return c.cli.Create(client.WithBearerToken(ctx, c.tok), in, opts...)
}
func (c vmregistryBearerClient) Destroy(ctx context.Context, in *vmregistrypb.DestroyRequest, opts ...grpc.CallOption) (*vmregistrypb.DestroyReply, error) {
	return c.cli.Destroy(client.WithBearerToken(ctx, c.tok), in, opts...)
}

func newVMRegistryClient(credstoreClient *client.CredstoreClient) (vmregistrypb.VMRegistryClient, error) {
	tok, err := credstoreClient.GetTokenForRemote(context.Background(), *vmregistryAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get vmregistry token: %v", err)
	}

	conn, err := clienthelpers.NewGRPCConn(*vmregistryAddress, *vmregistryCA, "", "")
	if err != nil {
		return nil, err
	}
	cli := &vmregistryBearerClient{
		cli: vmregistrypb.NewVMRegistryClient(conn),
		tok: tok,
	}
	return cli, nil
}

func main() {
	flag.Parse()
	defer glog.Flush()

	err := tracing.InitTracer("0.0.0.0:67", "microdhcpd")
	if err != nil {
		glog.Fatalf("failed to init tracing interface: %v", err)
	}

	cc, err := client.NewCredstoreClient(context.Background(), *credStoreAddress, *credStoreCA)
	if err != nil {
		glog.Fatalf("failed to init credstore: %v", err)
	}

	vmregClient, err := newVMRegistryClient(cc)
	if err != nil {
		glog.Fatalf("failed to connect to vmregistry: %v", err)
	}

	parsedServerIP := net.ParseIP(*serverIP)
	if parsedServerIP == nil {
		glog.Fatalf("failed to parse server ip %s", *serverIP)
	}

	_, netmask, err := net.ParseCIDR(*subnet)
	if err != nil {
		glog.Fatalf("failed to parse netmask: %v", err)
	}

	parsedGateway := net.ParseIP(*gateway)
	if parsedGateway == nil {
		glog.Fatalf("failed to parse gateway ip %s", *gateway)
	}

	parsedDNS := net.ParseIP(*dns)
	if parsedDNS == nil {
		glog.Fatalf("failed to parse dns ip %s", *dns)
	}

	svr := server.NewServer(
		vmregClient,
		parsedServerIP.To4(),
		netmask.Mask,
		parsedGateway.To4(),
		parsedDNS.To4(),
		*domainName)

	glog.Infof("listening on %s", *listenIf)
	err = dhcp4.ListenAndServeIf(*listenIf, &svr)
	glog.Fatalf("failed to listen and serve: %v", err)
}
