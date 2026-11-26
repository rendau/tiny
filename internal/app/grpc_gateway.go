package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/rendau/tiny/internal/errs"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/rs/cors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/rendau/tiny/internal/config"
)

// httpMaxBodySize — предел тела запроса к API админки. Самое большое, что сюда приходит, — ссылка
// с длинным адресом; у публичного сервера предел тот же (maxBodySize в internal/handler/public).
// Без предела шлюз читает тело в память целиком: запрос в сотни мегабайт — входа для этого не
// нужно — переполнял память пода, а с ним вставали и переходы по ссылкам (процесс общий).
const httpMaxBodySize = 1 << 20

// httpBodyTooLargeRep — ответ на слишком большое тело, в виде ErrorRep
const httpBodyTooLargeRep = `{"code":"invalid_request","message":"request body is too large","fields":{}}`

// limitBodyMiddleware не пускает запросы с телом больше limit. Длина объявлена в Content-Length —
// отказ 413 сразу, тело не читается. Не объявлена (тело идёт частями) — чтение обрывается на
// пределе, и обработчик получает ошибку чтения тела.
func limitBodyMiddleware(h http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = io.WriteString(w, httpBodyTooLargeRep)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, limit)
		h.ServeHTTP(w, r)
	})
}

func GrpcGatewayCreateHandler(muxHook func(*runtime.ServeMux) error) (http.Handler, error) {
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames:   true,
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
		runtime.WithErrorHandler(func(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
			var repBody []byte

			httpStatus := http.StatusBadRequest

			if st, ok := status.FromError(err); ok {
				// по 401 интерфейс обновляет токен и повторяет запрос
				switch st.Code() {
				case codes.Unauthenticated:
					httpStatus = http.StatusUnauthorized
				case codes.PermissionDenied:
					httpStatus = http.StatusForbidden
				}

				if st.Code() == codes.NotFound {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`service path not found`))
					return
				} else if len(st.Details()) > 0 {
					var marshalErr error
					repBody, marshalErr = marshaler.Marshal(st.Details()[0])
					if marshalErr != nil {
						slog.Error("GRPC_GW: ErrorHandler: Failed to marshal", "error", marshalErr)
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
				}
			}

			if len(repBody) == 0 {
				// runtime.DefaultHTTPErrorHandler(ctx, mux, marshaler, w, r, err)
				obj := map[string]string{
					"code":    errs.ServiceNA.Error(),
					"message": err.Error(),
				}
				repBody, err = json.Marshal(obj)
				if err != nil {
					slog.Error("GRPC_GW: ErrorHandler: Failed to marshal", "error", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}

			// slog.Error("GRPC_GW: ErrorHandler", "error", err)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(httpStatus)
			_, err = io.Copy(w, bytes.NewReader(repBody))
			if err != nil {
				slog.Error("GRPC_GW: ErrorHandler: Failed to write response", "error", err)
			}
			// _, _ = w.Write(repBody)
		}),
	)

	if muxHook != nil {
		err := muxHook(mux)
		if err != nil {
			return nil, fmt.Errorf("grpc-gateway: muxHook: %w", err)
		}
	}

	// предел тела запроса — до CORS: отказ 413 тоже уходит с заголовками CORS, интерфейс его прочитает
	handler := limitBodyMiddleware(mux, httpMaxBodySize)

	// add cors middleware
	if config.Conf.HttpCors {
		handler = cors.New(cors.Options{
			AllowOriginFunc: func(origin string) bool { return true },
			AllowedMethods: []string{
				http.MethodGet,
				http.MethodPut,
				http.MethodPost,
				http.MethodDelete,
			},
			AllowedHeaders: []string{
				"Accept",
				"Content-Type",
				"X-Requested-With",
				"Authorization",
			},
			AllowCredentials: true,
			MaxAge:           604800,
		}).Handler(handler)
	}

	// add recover middleware
	handler = func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				// use always new err instance in deferring
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
	}(handler)

	return handler, nil
}
