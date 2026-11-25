FROM alpine:latest

RUN apk add --no-cache --upgrade ca-certificates tzdata curl

WORKDIR /app

COPY ./cmd/build/. ./
COPY ./docs ./docs
COPY ./migrations ./migrations
COPY ./admin-ui ./admin-ui

CMD ["./svc"]
