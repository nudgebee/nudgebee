FROM golang:1.26-alpine AS build-stage

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . ./
RUN go build -o /app/nudgebee-relay-server ./cmd
ENV GIN_MODE=release
RUN chmod +x /app/nudgebee-relay-server
EXPOSE 8080
CMD [ "./nudgebee-relay-server" ]


FROM alpine:3.19 AS release-stage
WORKDIR /app
COPY --from=build-stage /app/nudgebee-relay-server /app/nudgebee-relay-server
RUN chmod +x /app/nudgebee-relay-server
EXPOSE 8080
CMD ["./nudgebee-relay-server"]
