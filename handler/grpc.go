/*
Copyright (C)  2018 Yahoo Japan Corporation Athenz team.

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
package handler

import (
	"context"

	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/yahoojapan/authorization-proxy/v4/config"
	"github.com/yahoojapan/authorization-proxy/v4/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func NewGRPC(cfg config.Proxy, prov service.Authorizationd) *grpc.Server {
	return grpc.NewServer(
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if ok {
				rs := md.Get(cfg.RoleHeader)
				if rs != nil && len(rs) > 0 {
					// Decide on which backend to dial
					if val, exists := md[":authority"]; exists && val[0] == "staging.api.example.com" {
						// Make sure we use DialContext so the dialing can be cancelled/time out together with the context.
						conn, err := grpc.DialContext(ctx, "api-service.staging.svc.local", grpc.WithCodec(proxy.Codec()))
						return ctx, conn, err
					} else if val, exists := md[":authority"]; exists && val[0] == "api.example.com" {
						conn, err := grpc.DialContext(ctx, "api-service.prod.svc.local", grpc.WithCodec(proxy.Codec()))
						return ctx, conn, err
					}
				}

			}
			return ctx, nil, grpc.Errorf(codes.Unimplemented, ErrRPCMetadataNotFound)
		})))
}
