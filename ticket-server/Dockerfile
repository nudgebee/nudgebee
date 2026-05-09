FROM golang:1.26-alpine as build-stage


# Set destination for COPY
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

COPY ./ ./

# Build
RUN CGO_ENABLED=0 GOOS=linux go build  -o /app/ticket-server *.go
RUN chmod +x /app/ticket-server
RUN rm -rf /go

EXPOSE 8000
CMD ["/app/ticket-server"]


FROM alpine:3.19 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/ticket-server /app/ticket-server
RUN chmod +x /app/ticket-server
EXPOSE 8000
CMD ["/app/ticket-server"]
