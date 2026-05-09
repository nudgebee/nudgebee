FROM golang:1.26-alpine AS build-stage

# Set destination for COPY
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

COPY ./ ./

# Build
RUN CGO_ENABLED=0 GOOS=linux go build  -o /app/services cmd/*.go
RUN chmod +x /app/services
RUN go install github.com/google/pprof@latest
EXPOSE 8000
CMD ["/app/services"]

FROM alpine:3.19 AS release-stage
RUN apk add git graphviz
WORKDIR /app
COPY --from=build-stage /app/services /app/services
COPY --from=build-stage /go/bin/pprof /usr/local/bin/pprof
COPY --from=build-stage /app/knowledge_graph/core/default_relationships.json /app/knowledge_graph/core/default_relationships.json
RUN chmod +x /app/services
EXPOSE 8000
CMD ["/app/services"]
