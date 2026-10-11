# status-api — tiny Go API (status + message). Multi-stage build.
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY app/go.mod ./
COPY app/main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/status-api .

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/status-api /status-api
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/status-api"]
