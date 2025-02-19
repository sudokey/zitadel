package api

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/zitadel/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	internal_authz "github.com/zitadel/zitadel/internal/api/authz"
	"github.com/zitadel/zitadel/internal/api/grpc/server"
	http_util "github.com/zitadel/zitadel/internal/api/http"
	"github.com/zitadel/zitadel/internal/api/ui/login"
	"github.com/zitadel/zitadel/internal/errors"
	"github.com/zitadel/zitadel/internal/logstore"
	"github.com/zitadel/zitadel/internal/query"
	"github.com/zitadel/zitadel/internal/telemetry/metrics"
	"github.com/zitadel/zitadel/internal/telemetry/tracing"
)

type API struct {
	port          uint16
	grpcServer    *grpc.Server
	verifier      *internal_authz.TokenVerifier
	health        healthCheck
	router        *mux.Router
	http1HostName string
	grpcGateway   *server.Gateway
	healthServer  *health.Server
}

type healthCheck interface {
	Health(ctx context.Context) error
}

func New(
	ctx context.Context,
	port uint16,
	router *mux.Router,
	queries *query.Queries,
	verifier *internal_authz.TokenVerifier,
	authZ internal_authz.Config,
	tlsConfig *tls.Config, http2HostName, http1HostName string,
	accessSvc *logstore.Service,
) (_ *API, err error) {
	api := &API{
		port:          port,
		verifier:      verifier,
		health:        queries,
		router:        router,
		http1HostName: http1HostName,
	}

	api.grpcServer = server.CreateServer(api.verifier, authZ, queries, http2HostName, tlsConfig, accessSvc)
	api.grpcGateway, err = server.CreateGateway(ctx, port, http1HostName)
	if err != nil {
		return nil, err
	}
	api.registerHealthServer()

	api.RegisterHandlerOnPrefix("/debug", api.healthHandler())
	api.router.Handle("/", http.RedirectHandler(login.HandlerPrefix, http.StatusFound))

	return api, nil
}

// RegisterServer registers a grpc service on the grpc server,
// creates a new grpc gateway and registers it as a separate http handler
//
// used for v1 api (system, admin, mgmt, auth)
func (a *API) RegisterServer(ctx context.Context, grpcServer server.WithGatewayPrefix) error {
	grpcServer.RegisterServer(a.grpcServer)
	handler, prefix, err := server.CreateGatewayWithPrefix(ctx, grpcServer, a.port, a.http1HostName)
	if err != nil {
		return err
	}
	a.RegisterHandlerOnPrefix(prefix, handler)
	a.verifier.RegisterServer(grpcServer.AppName(), grpcServer.MethodPrefix(), grpcServer.AuthMethods())
	a.healthServer.SetServingStatus(grpcServer.MethodPrefix(), healthpb.HealthCheckResponse_SERVING)
	return nil
}

// RegisterService registers a grpc service on the grpc server,
// and its gateway on the gateway handler
//
// used for >= v2 api (e.g. user, session, ...)
func (a *API) RegisterService(ctx context.Context, grpcServer server.Server) error {
	grpcServer.RegisterServer(a.grpcServer)
	err := server.RegisterGateway(ctx, a.grpcGateway, grpcServer)
	if err != nil {
		return err
	}
	a.verifier.RegisterServer(grpcServer.AppName(), grpcServer.MethodPrefix(), grpcServer.AuthMethods())
	a.healthServer.SetServingStatus(grpcServer.MethodPrefix(), healthpb.HealthCheckResponse_SERVING)
	return nil
}

// RegisterHandlerOnPrefix registers a http handler on a path prefix
// the prefix will not be passed to the actual handler
func (a *API) RegisterHandlerOnPrefix(prefix string, handler http.Handler) {
	prefix = strings.TrimSuffix(prefix, "/")
	subRouter := a.router.PathPrefix(prefix).Name(prefix).Subrouter()
	subRouter.PathPrefix("").Handler(http.StripPrefix(prefix, handler))
}

// RegisterHandlerPrefixes registers a http handler on a multiple path prefixes
// the prefix will remain when calling the actual handler
func (a *API) RegisterHandlerPrefixes(handler http.Handler, prefixes ...string) {
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		subRouter := a.router.PathPrefix(prefix).Name(prefix).Subrouter()
		subRouter.PathPrefix("").Handler(handler)
	}
}

func (a *API) registerHealthServer() {
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(a.grpcServer, healthServer)
	a.healthServer = healthServer
}

func (a *API) RouteGRPC() {
	http2Route := a.router.
		MatcherFunc(func(r *http.Request, _ *mux.RouteMatch) bool {
			return r.ProtoMajor == 2
		}).
		Subrouter().
		Name("grpc")
	http2Route.
		Methods(http.MethodPost).
		Headers("Content-Type", "application/grpc").
		Handler(a.grpcServer)

	a.routeGRPCWeb()
	a.router.NewRoute().
		Handler(a.grpcGateway.Handler()).
		Name("grpc-gateway")
}

func (a *API) routeGRPCWeb() {
	grpcWebServer := grpcweb.WrapServer(a.grpcServer,
		grpcweb.WithAllowedRequestHeaders(
			[]string{
				http_util.Origin,
				http_util.ContentType,
				http_util.Accept,
				http_util.AcceptLanguage,
				http_util.Authorization,
				http_util.ZitadelOrgID,
				http_util.XUserAgent,
				http_util.XGrpcWeb,
			},
		),
		grpcweb.WithOriginFunc(func(_ string) bool {
			return true
		}),
	)
	a.router.NewRoute().
		Methods(http.MethodPost, http.MethodOptions).
		MatcherFunc(
			func(r *http.Request, _ *mux.RouteMatch) bool {
				return grpcWebServer.IsGrpcWebRequest(r) || grpcWebServer.IsAcceptableGrpcCorsRequest(r)
			}).
		Handler(grpcWebServer).
		Name("grpc-web")
}

func (a *API) healthHandler() http.Handler {
	checks := []ValidationFunction{
		func(ctx context.Context) error {
			if err := a.health.Health(ctx); err != nil {
				return errors.ThrowInternal(err, "API-F24h2", "DB CONNECTION ERROR")
			}
			return nil
		},
	}
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", handleHealth)
	handler.HandleFunc("/ready", handleReadiness(checks))
	handler.HandleFunc("/validate", handleValidate(checks))
	handler.Handle("/metrics", metricsExporter())

	return handler
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	_, err := w.Write([]byte("ok"))
	logging.WithFields("traceID", tracing.TraceIDFromCtx(r.Context())).OnError(err).Error("error writing ok for health")
}

func handleReadiness(checks []ValidationFunction) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		errs := validate(r.Context(), checks)
		if len(errs) == 0 {
			http_util.MarshalJSON(w, "ok", nil, http.StatusOK)
			return
		}
		http_util.MarshalJSON(w, nil, errs[0], http.StatusPreconditionFailed)
	}
}

func handleValidate(checks []ValidationFunction) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		errs := validate(r.Context(), checks)
		if len(errs) == 0 {
			http_util.MarshalJSON(w, "ok", nil, http.StatusOK)
			return
		}
		http_util.MarshalJSON(w, errs, nil, http.StatusOK)
	}
}

type ValidationFunction func(ctx context.Context) error

func validate(ctx context.Context, validations []ValidationFunction) []error {
	errs := make([]error, 0)
	for _, validation := range validations {
		if err := validation(ctx); err != nil {
			logging.WithFields("traceID", tracing.TraceIDFromCtx(ctx)).WithError(err).Error("validation failed")
			errs = append(errs, err)
		}
	}
	return errs
}

func metricsExporter() http.Handler {
	exporter := metrics.GetExporter()
	if exporter == nil {
		return http.NotFoundHandler()
	}
	return exporter
}
