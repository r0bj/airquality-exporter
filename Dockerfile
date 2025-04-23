FROM golang:1.24.2 AS builder

WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY *.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o airquality-exporter .


FROM scratch

COPY --from=builder /workspace/airquality-exporter /

ENTRYPOINT ["/airquality-exporter"]
