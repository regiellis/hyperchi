# syntax=docker/dockerfile:1.10
# Build from the repository root: docker build -f examples/showcase/Dockerfile .
FROM golang:1.26.8-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY hyperchi ./hyperchi
COPY modules/templ ./modules/templ
COPY modules/jet ./modules/jet
COPY modules/markdown ./modules/markdown
COPY examples/showcase/go.mod examples/showcase/go.sum ./examples/showcase/
WORKDIR /src/examples/showcase
RUN go mod download
COPY examples/showcase/*.go ./
COPY examples/showcase/static ./static
COPY examples/showcase/marketing ./marketing
COPY examples/showcase/docs ./docs
COPY examples/showcase/reference ./reference
COPY examples/showcase/internal ./internal
ARG RELEASE=development
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.releaseVersion=${RELEASE}" -o /out/site .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 site
WORKDIR /app
COPY --from=builder /out/site ./site
COPY examples/showcase/templates ./templates
USER site
ENV HOST=0.0.0.0 PORT=8080 ENVIRONMENT=production
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["./site"]
