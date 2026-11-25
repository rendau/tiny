package app

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rendau/tiny/internal/errs"
)

const metricProtocolPublic = "public"

// PublicHttpServerCreate — публичный сервер, совместимый с Shlink: переход по короткой ссылке и
// /rest/v3/* (internal/handler/public)
func PublicHttpServerCreate(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		Handler:           recoverMiddleware(handler),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 * 1024,
	}
}

// publicObserve — учёт запроса публичного сервера в тех же метриках, что и gRPC: method — имя
// маршрута, code — ok или код отказа. Отказ по делу (не найдено, неверный запрос, нет ключа)
// отличается от сбоя (5xx) — как у errs.IsFailure.
func publicObserve(route string, httpStatus int, duration time.Duration) {
	responseStatus, code := "ok", "ok"
	if httpStatus >= http.StatusBadRequest {
		responseStatus, code = "error", publicErrCode(httpStatus)
	}

	metricRequestCounter.WithLabelValues(metricProtocolPublic, route, responseStatus, code).Inc()
	metricResponseDuration.WithLabelValues(metricProtocolPublic, route).Observe(duration.Seconds())
}

func publicErrCode(httpStatus int) string {
	switch {
	case httpStatus == http.StatusNotFound:
		return errs.ObjectNotFound.Error()
	case httpStatus == http.StatusUnauthorized:
		return errs.NotAuthorized.Error()
	case httpStatus >= http.StatusInternalServerError:
		return errs.ServiceNA.Error()
	case httpStatus >= http.StatusBadRequest:
		return errs.InvalidRequest.Error()
	}
	return strconv.Itoa(httpStatus)
}
