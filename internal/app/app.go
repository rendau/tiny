package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rendau/tiny/internal/config"
	"github.com/rendau/tiny/internal/constant"
	domainLinkRepoDbP "github.com/rendau/tiny/internal/domain/link/repo/db"
	domainLinkServiceP "github.com/rendau/tiny/internal/domain/link/service"
	domainSessionServiceP "github.com/rendau/tiny/internal/domain/session/service"
	domainUsrRepoDbP "github.com/rendau/tiny/internal/domain/usr/repo/db"
	domainUsrServiceP "github.com/rendau/tiny/internal/domain/usr/service"
	handlerGrpcP "github.com/rendau/tiny/internal/handler/grpc"
	handlerPublicP "github.com/rendau/tiny/internal/handler/public"
	"github.com/rendau/tiny/internal/infra/metrics"
	serviceLoginlimitServiceP "github.com/rendau/tiny/internal/service/loginlimit/service"
	usecaseLinkP "github.com/rendau/tiny/internal/usecase/link"
	usecaseUsrP "github.com/rendau/tiny/internal/usecase/usr"
	"github.com/rendau/tiny/pkg/proto/tiny_v1"
)

type App struct {
	globalTracerCloser io.Closer

	pgpool *pgxpool.Pool

	grpcServer       *GrpcServer
	httpServer       *http.Server
	publicHttpServer *http.Server
	adminHttpServer  *http.Server
	systemHttpServer *http.Server

	ctx       context.Context
	ctxCancel context.CancelFunc

	exitCode int
}

func (a *App) Init() {
	var err error

	a.ctx, a.ctxCancel = context.WithCancel(context.Background())

	// logger
	initLogger(config.Conf.Debug, config.Conf.LogLevel)

	// globalTracer
	{
		if config.Conf.WithTracing && config.Conf.JaegerAddress != "" {
			slog.Info("tracing enabled")
			_, a.globalTracerCloser, err = tracerInitGlobal(config.Conf.JaegerAddress, constant.ServiceName)
			errCheck(err, "tracerInitGlobal")
		}
	}

	// pgpool
	a.pgpool, err = initPgPool(config.Conf.PgDsn)
	errCheck(err, "pgpool init")

	// migrations
	{
		runMigrations()
		slog.Info("PG-migrations have been successfully applied")
	}

	// session (вход в админку — только свои учётки)
	if config.Conf.JwtSecret == "" {
		errCheck(errors.New("JWT_SECRET is empty"), "session init")
	}
	sessionService := domainSessionServiceP.New(config.Conf.JwtSecret)

	// usr
	usrRepo := domainUsrRepoDbP.New(a.pgpool)
	usrService := domainUsrServiceP.New(usrRepo)
	usrUsecase := usecaseUsrP.New(usrService, sessionService, serviceLoginlimitServiceP.New(5, time.Minute))
	usrHandler := handlerGrpcP.NewUsr(usrUsecase)

	// link
	linkRepo := domainLinkRepoDbP.New(a.pgpool)
	linkService := domainLinkServiceP.New(linkRepo)
	linkUsecase := usecaseLinkP.New(linkService, sessionService, usecaseLinkP.Config{
		DefaultDomain:     config.Conf.DefaultDomain,
		HttpsEnabled:      config.Conf.IsHttpsEnabled,
		DefaultCodeLength: config.Conf.DefaultShortCodesLength,
	})
	linkHandler := handlerGrpcP.NewLink(linkUsecase)
	tagHandler := handlerGrpcP.NewTag(linkUsecase)

	// grpc server
	{
		a.grpcServer = NewGrpcServer("main", sessionService, func(server *grpc.Server) {
			tiny_v1.RegisterUsrServiceServer(server, usrHandler)
			tiny_v1.RegisterLinkServiceServer(server, linkHandler)
			tiny_v1.RegisterTagServiceServer(server, tagHandler)
		})
	}

	// http-gw server
	{
		var handler http.Handler

		handler, err = GrpcGatewayCreateHandler(func(mux *runtime.ServeMux) error {
			opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

			var conn *grpc.ClientConn
			conn, err = grpc.NewClient("localhost:"+config.Conf.GrpcPort, opts...)
			errCheck(err, "grpc.Dial")

			// register grpc handlers
			handlers := []func(context.Context, *runtime.ServeMux, *grpc.ClientConn) error{
				tiny_v1.RegisterUsrServiceHandler,
				tiny_v1.RegisterLinkServiceHandler,
				tiny_v1.RegisterTagServiceHandler,
			}
			for _, h := range handlers {
				err = h(context.Background(), mux, conn)
				if err != nil {
					return fmt.Errorf("grpc-gateway: register grpc-handler: %w", err)
				}
			}

			return nil
		})
		errCheck(err, "grpcGatewayCreateHandler")

		// server
		a.httpServer = &http.Server{
			Addr:              ":" + config.Conf.HttpPort,
			Handler:           handler,
			ReadHeaderTimeout: 2 * time.Second,
			ReadTimeout:       time.Minute,
			MaxHeaderBytes:    300 * 1024,
		}
	}

	// public http server (переход по короткой ссылке и /rest/v3/*, совместимые с Shlink)
	{
		errCheck(handlerPublicP.CheckApiKeyHashes(config.Conf.ApiKeyHashes), "API_KEY_HASHES")
		if len(config.Conf.ApiKeys) == 0 && len(config.Conf.ApiKeyHashes) == 0 {
			slog.Warn("API_KEYS and API_KEY_HASHES are empty: /rest/* answers 401 to everyone")
		}

		handlerCfg := handlerPublicP.Config{
			ApiKeys:           config.Conf.ApiKeys,
			ApiKeyHashes:      config.Conf.ApiKeyHashes,
			LogCreateRequests: config.Conf.LogCreateRequests,
		}
		if metrics.Enabled {
			handlerCfg.Observe = publicObserve
		}

		a.publicHttpServer = PublicHttpServerCreate(config.Conf.PublicPort, handlerPublicP.New(linkUsecase, handlerCfg))
	}

	// admin http server (интерфейс админки)
	{
		a.adminHttpServer = AdminHttpServerCreate(config.Conf.AdminPort, config.Conf.AdminDir, AdminConfig{
			ApiBaseUrl: config.Conf.AdminApiBaseUrl,
			Auth:       AdminAuthConfig{Mode: adminAuthModeLocal},
		})
	}

	// system http server (healthcheck, docs, metrics)
	{
		a.systemHttpServer = SystemHttpServerCreate()
	}
}

func (a *App) PreStartHook() {
	slog.Info("PreStartHook")
}

func (a *App) Start() {
	slog.Info("Starting", "version", constant.Version, "commit", constant.Commit, "built_at", constant.BuiltAt)

	// grpc server
	{
		err := a.grpcServer.Start()
		errCheck(err, "grpcServer.Start")
	}

	// http-gw server
	a.startHttpServer(a.httpServer, "http-server")

	// public http server
	a.startHttpServer(a.publicHttpServer, "public-http-server")

	// admin http server
	if a.adminHttpServer != nil {
		a.startHttpServer(a.adminHttpServer, "admin-http-server")
	}

	// system http server
	a.startHttpServer(a.systemHttpServer, "system-http-server")
}

func (a *App) Listen() {
	signalCtx, signalCtxCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalCtxCancel()

	// wait signal
	<-signalCtx.Done()
}

func (a *App) Stop() {
	slog.Info("Shutting down...")

	// stop context
	a.ctxCancel()

	// http-gw server
	a.stopHttpServer(a.httpServer, "http-server")

	// public http server
	a.stopHttpServer(a.publicHttpServer, "public-http-server")

	// admin http server
	if a.adminHttpServer != nil {
		a.stopHttpServer(a.adminHttpServer, "admin-http-server")
	}

	// system http server
	a.stopHttpServer(a.systemHttpServer, "system-http-server")

	// grpc server
	a.grpcServer.Stop()
}

func (a *App) WaitJobs() {
	slog.Info("waiting jobs")
}

func (a *App) Exit() {
	slog.Info("Exit")

	if a.globalTracerCloser != nil {
		_ = a.globalTracerCloser.Close()
	}

	a.pgpool.Close()

	// flush stdout

	os.Exit(a.exitCode)
}

// startHttpServer запускает сервер в фоне; занятый порт или другой сбой прослушивания — фатально
func (a *App) startHttpServer(server *http.Server, name string) {
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCheck(err, name+" stopped")
		}
	}()
	slog.Info(name + " started " + server.Addr)
}

// stopHttpServer даёт серверу до 15 секунд дообслужить начатые запросы
func (a *App) stopHttpServer(server *http.Server, name string) {
	ctx, ctxCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer ctxCancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error(name+" shutdown error", "error", err)
		a.exitCode = 1
	}
}

func errCheck(err error, msg string) {
	if err != nil {
		if msg != "" {
			err = fmt.Errorf("%s: %w", msg, err)
		}
		slog.Error(err.Error())
		os.Exit(1)
	}
}

// recoverMiddleware — паника в обработчике запроса не роняет сервис
func recoverMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error(
					"Recovered from panic",
					slog.Any("error", err),
					slog.Any("recovery_stacktrace", string(debug.Stack())),
				)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	})
}
