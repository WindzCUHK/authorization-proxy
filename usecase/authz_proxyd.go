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

package usecase

import (
	"context"

	"github.com/kpango/glg"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/yahoojapan/authorization-proxy/config"
	"github.com/yahoojapan/authorization-proxy/handler"
	"github.com/yahoojapan/authorization-proxy/infra"
	"github.com/yahoojapan/authorization-proxy/router"
	"github.com/yahoojapan/authorization-proxy/service"

	authorizerd "github.com/yahoojapan/athenz-authorizer/v2"
)

// AuthzProxyDaemon represents Authorization Proxy daemon behavior.
type AuthzProxyDaemon interface {
	Start(ctx context.Context) <-chan []error
}

type authzProxyDaemon struct {
	cfg    config.Config
	athenz service.Authorizationd
	server service.Server
}

// New returns a Authorization Proxy daemon, or error occurred.
// The daemon contains a token service authentication and authorization server.
// This function will also initialize the mapping rules for the authentication and authorization check.
func New(cfg config.Config) (AuthzProxyDaemon, error) {
	athenz, err := newAuthzD(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "cannot newAuthzD(cfg)")
	}

	debugMux := router.NewDebugRouter(cfg.Server, athenz)

	return &authzProxyDaemon{
		cfg:    cfg,
		athenz: athenz,
		server: service.NewServer(
			service.WithServerConfig(cfg.Server),
			service.WithServerHandler(handler.New(cfg.Proxy, infra.NewBuffer(cfg.Proxy.BufferSize), athenz)),
			service.WithDebugHandler(debugMux)),
	}, nil
}

// Start returns a channel of error slice . This error channel reports the errors inside the Authorizer daemon and the Authorization Proxy server.
func (g *authzProxyDaemon) Start(ctx context.Context) <-chan []error {
	ech := make(chan []error)
	var emap map[string]uint64 // used for returning value from child goroutine, should not r/w in this goroutine
	var eg *errgroup.Group
	eg, ctx = errgroup.WithContext(ctx)

	// handle authorizer daemon error, return on channel close
	eg.Go(func() error {
		// closure, only this goroutine should write on the variable and the map
		emap = make(map[string]uint64, 1)
		pch := g.athenz.Start(ctx)

		for err := range pch {
			if err != nil {
				glg.Errorf("pch: %v", err)
				// count errors by cause
				cause := errors.Cause(err).Error()
				_, ok := emap[cause]
				if !ok {
					emap[cause] = 1
				} else {
					emap[cause]++
				}
			}
		}

		return nil
	})

	// handle proxy server error, return on server shutdown done
	eg.Go(func() error {
		errs := <-g.server.ListenAndServe(ctx)
		glg.Errorf("sch: %v", errs)

		if len(errs) == 0 {
			// cannot be nil so that the context can cancel
			return errors.New("")
		}
		var baseErr error
		for i, err := range errs {
			if i == 0 {
				baseErr = err
			} else {
				baseErr = errors.Wrap(baseErr, err.Error())
			}
		}
		return baseErr
	})

	// wait for shutdown, and summarize errors
	go func() {
		defer close(ech)

		<-ctx.Done()
		err := eg.Wait()

		/*
			Read on emap is safe here, if and only if:
			1. emap is not used in the parenet goroutine
			2. the writer goroutine returns only if all erros are written, i.e. pch is closed
			3. this goroutine should wait for the writer goroutine to end, i.e. eg.Wait()
		*/
		// aggregate all errors as array
		perrs := make([]error, 0, len(emap))
		for errMsg, count := range emap {
			perrs = append(perrs, errors.WithMessagef(errors.New(errMsg), "authorizerd: %d times appeared", count))
		}

		// proxy server go func, should always return not nil error
		ech <- append(perrs, err)
	}()

	return ech
}

func newAuthzD(cfg config.Config) (service.Authorizationd, error) {
	return authorizerd.New(
		authorizerd.WithAthenzURL(cfg.Athenz.URL),

		authorizerd.WithPubkeyRefreshDuration(cfg.Authorization.PubKeyRefreshDuration),
		authorizerd.WithPubkeySysAuthDomain(cfg.Authorization.PubKeySysAuthDomain),
		authorizerd.WithPubkeyEtagExpTime(cfg.Authorization.PubKeyEtagExpTime),
		authorizerd.WithPubkeyEtagFlushDuration(cfg.Authorization.PubKeyEtagFlushDur),
		authorizerd.WithPubkeyErrRetryInterval(cfg.Authorization.PubKeyErrRetryInterval),
		authorizerd.WithAthenzDomains(cfg.Authorization.AthenzDomains...),

		authorizerd.WithPolicyExpireMargin(cfg.Authorization.PolicyExpireMargin),
		authorizerd.WithPolicyRefreshDuration(cfg.Authorization.PolicyRefreshDuration),
		authorizerd.WithPolicyEtagFlushDuration(cfg.Authorization.PolicyEtagFlushDur),
		authorizerd.WithPolicyErrRetryInterval(cfg.Authorization.PolicyErrRetryInterval),

		authorizerd.WithDisableJwkd(),
	)
}