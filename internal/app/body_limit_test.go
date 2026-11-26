package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	domainSessionServiceP "github.com/rendau/tiny/internal/domain/session/service"
)

// chunkedBody прячет длину тела: запрос уходит без Content-Length, частями
type chunkedBody struct{ io.Reader }

func TestLimitBodyMiddleware(t *testing.T) {
	const limit = 64

	var (
		called  bool
		body    []byte
		readErr error
	)
	h := limitBodyMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		body, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusBadRequest)
		}
	}), limit)

	serve := func(reqBody io.Reader) *httptest.ResponseRecorder {
		called, body, readErr = false, nil, nil
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/usr/login", reqBody))
		return w
	}

	t.Run("тело в пределе проходит целиком", func(t *testing.T) {
		w := serve(strings.NewReader(strings.Repeat("a", limit)))

		require.True(t, called)
		require.NoError(t, readErr)
		assert.Len(t, body, limit)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("объявленная длина больше предела — 413, тело не читается", func(t *testing.T) {
		w := serve(strings.NewReader(strings.Repeat("a", limit+1)))

		assert.False(t, called)
		assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"code":"invalid_request","message":"request body is too large","fields":{}}`, w.Body.String())
	})

	t.Run("длина не объявлена — чтение обрывается на пределе", func(t *testing.T) {
		w := serve(chunkedBody{strings.NewReader(strings.Repeat("a", 10*limit))})

		require.True(t, called)
		_, tooLarge := errors.AsType[*http.MaxBytesError](readErr)
		assert.True(t, tooLarge, "readErr: %v", readErr)
		assert.LessOrEqual(t, len(body), limit)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("запрос без тела проходит", func(t *testing.T) {
		w := serve(nil)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// Предел стоит в самом обработчике API админки, а не только в отдельной функции
func TestGrpcGatewayBodyLimit(t *testing.T) {
	h, err := GrpcGatewayCreateHandler(nil)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/usr/login", strings.NewReader(strings.Repeat("a", httpMaxBodySize+1))))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

// gRPC-порт: сообщение больше grpcMaxRecvMsgSize сервер не принимает
func TestGrpcServerRecvLimit(t *testing.T) {
	srv := NewGrpcServer("test", domainSessionServiceP.New("test-secret"), func(server *grpc.Server) {
		grpc_health_v1.RegisterHealthServer(server, health.NewServer())
	})

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.server.Serve(lis) }()
	t.Cleanup(srv.server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := grpc_health_v1.NewHealthClient(conn)

	check := func(size int) codes.Code {
		_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{Service: strings.Repeat("a", size)})
		return status.Code(err)
	}

	// в пределе — запрос дошёл до обработчика: тот не знает такой службы, а перехватчик ошибок
	// сервера отдаёт любой отказ как InvalidArgument
	assert.Equal(t, codes.InvalidArgument, check(grpcMaxRecvMsgSize/2))
	// больше предела — отказ до обработчика
	assert.Equal(t, codes.ResourceExhausted, check(grpcMaxRecvMsgSize+1))
}
